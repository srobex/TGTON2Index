package ton

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"go.uber.org/zap"
)

const (
	// URL глобального конфига TON
	mainnetConfigURL = "https://ton.org/global-config.json"
	testnetConfigURL = "https://ton.org/testnet-global.config.json"

	// Интервал опроса новых блоков
	blockPollInterval = 100 * time.Millisecond

	// Таймаут на получение блока
	blockTimeout = 5 * time.Second

	// Количество воркеров = GOMAXPROCS * 4
	workerMultiplier = 4
)

// Event описывает данные о транзакции с деплоем.
type Event struct {
	AccountAddress string
	CodeHash       string
	Timestamp      time.Time
	Seqno          uint32
	Workchain      int32
	Shard          uint64
	TxHash         string
	TxLT           uint64
	IsDeploy       bool
	BlockUnixtime  int64
}

// Handler получает события из индексатора.
type Handler func(event Event) error

// Client определяет контракт для работы с TON.
type Client interface {
	Start(ctx context.Context) error
	Subscribe(ctx context.Context, handler Handler) error
	Catchup(ctx context.Context, since time.Time, handler Handler) error
	RunGetMethod(ctx context.Context, address string, method string, stack ...any) ([][]byte, error)
	GetCodeHash(ctx context.Context, address string) (string, error)
}

// LatencyStats хранит статистику по задержкам.
type LatencyStats struct {
	TotalEvents    int64
	TotalLatencyMs int64
	MinLatencyMs   int64
	MaxLatencyMs   int64
	LastLatencyMs  int64
}

// IndexerClient — высокоскоростной клиент для индексации TON.
type IndexerClient struct {
	network     string
	liteservers []string
	logger      *zap.Logger

	pool         *liteclient.ConnectionPool
	api          ton.APIClientWrapped
	mu           sync.RWMutex
	lastMC       uint32
	shardWorkers int

	stats        LatencyStats
	blocksTotal  int64
	txTotal      int64
	deploysTotal int64
}

// NewIndexerClient создаёт клиента для выбранной сети.
func NewIndexerClient(network string, liteservers []string, logger *zap.Logger) *IndexerClient {
	workers := runtime.GOMAXPROCS(0) * workerMultiplier
	if workers < 8 {
		workers = 8
	}
	if workers > 64 {
		workers = 64
	}

	return &IndexerClient{
		network:      network,
		liteservers:  liteservers,
		logger:       logger,
		shardWorkers: workers,
		stats: LatencyStats{
			MinLatencyMs: 999999,
		},
	}
}

// Start подключается к liteserver'ам.
func (c *IndexerClient) Start(ctx context.Context) error {
	c.pool = liteclient.NewConnectionPool()

	configURL := mainnetConfigURL
	if c.network == "testnet" {
		configURL = testnetConfigURL
	}

	c.logger.Info("загружаем конфигурацию TON", zap.String("url", configURL))

	cfg, err := fetchGlobalConfig(ctx, configURL)
	if err != nil {
		return fmt.Errorf("не удалось загрузить глобальный конфиг: %w", err)
	}

	if err := c.pool.AddConnectionsFromConfig(ctx, cfg); err != nil {
		return fmt.Errorf("не удалось подключиться к liteservers: %w", err)
	}

	// Создаём API клиент
	c.api = ton.NewAPIClient(c.pool, ton.ProofCheckPolicyFast).WithRetry()

	c.logger.Info("подключение к TON установлено",
		zap.String("network", c.network),
		zap.Int("shard_workers", c.shardWorkers),
	)
	return nil
}

// Subscribe подключается к потоку новых блоков в реальном времени.
func (c *IndexerClient) Subscribe(ctx context.Context, handler Handler) error {
	if c.api == nil {
		return fmt.Errorf("API клиент не инициализирован")
	}

	c.logger.Info("запускаем подписку на новые блоки",
		zap.Duration("poll_interval", blockPollInterval),
	)

	// Получаем текущий мастерчейн блок
	master, err := c.api.CurrentMasterchainInfo(ctx)
	if err != nil {
		return fmt.Errorf("не удалось получить текущий мастерчейн: %w", err)
	}

	c.mu.Lock()
	c.lastMC = master.SeqNo
	c.mu.Unlock()

	c.logger.Info("начинаем с блока мастерчейна", zap.Uint32("seqno", master.SeqNo))

	// Основной цикл опроса новых блоков
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("подписка остановлена")
			return ctx.Err()
		default:
		}

		// Получаем последний мастерчейн блок
		newMaster, err := c.api.CurrentMasterchainInfo(ctx)
		if err != nil {
			c.logger.Warn("ошибка получения мастерчейна", zap.Error(err))
			time.Sleep(blockPollInterval)
			continue
		}

		c.mu.RLock()
		lastSeqno := c.lastMC
		c.mu.RUnlock()

		// Если новый блок появился — обрабатываем
		if newMaster.SeqNo > lastSeqno {
			startTime := time.Now()

			for seqno := lastSeqno + 1; seqno <= newMaster.SeqNo; seqno++ {
				if err := c.processBlock(ctx, seqno, handler); err != nil {
					c.logger.Warn("ошибка обработки блока",
						zap.Uint32("seqno", seqno),
						zap.Error(err),
					)
				}
				atomic.AddInt64(&c.blocksTotal, 1)
			}

			c.mu.Lock()
			c.lastMC = newMaster.SeqNo
			c.mu.Unlock()

			processingTime := time.Since(startTime)

			if newMaster.SeqNo%100 == 0 || processingTime > time.Second {
				c.logger.Info("блоки обработаны",
					zap.Uint32("seqno", newMaster.SeqNo),
					zap.Duration("processing_time", processingTime),
					zap.Int64("blocks_total", atomic.LoadInt64(&c.blocksTotal)),
					zap.Int64("deploys_total", atomic.LoadInt64(&c.deploysTotal)),
					zap.Int64("avg_latency_ms", c.getAvgLatency()),
				)
			}
		}

		time.Sleep(blockPollInterval)
	}
}

// processBlock обрабатывает один блок мастерчейна и все его шарды.
func (c *IndexerClient) processBlock(ctx context.Context, seqno uint32, handler Handler) error {
	blockCtx, cancel := context.WithTimeout(ctx, blockTimeout)
	defer cancel()

	// Получаем информацию о блоке мастерчейна
	// Shard для мастерчейна: -9223372036854775808 (минимальный int64)
	masterInfo, err := c.api.LookupBlock(blockCtx, -1, -9223372036854775808, seqno)
	if err != nil {
		return fmt.Errorf("не удалось найти блок %d: %w", seqno, err)
	}

	// Получаем все шарды этого блока
	shards, err := c.api.GetBlockShardsInfo(blockCtx, masterInfo)
	if err != nil {
		return fmt.Errorf("не удалось получить шарды блока %d: %w", seqno, err)
	}

	// Параллельная обработка шардов
	var wg sync.WaitGroup
	shardChan := make(chan *ton.BlockIDExt, len(shards)+1)

	numWorkers := c.shardWorkers
	if numWorkers > len(shards)+1 {
		numWorkers = len(shards) + 1
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shard := range shardChan {
				shardCtx, shardCancel := context.WithTimeout(ctx, blockTimeout)
				if err := c.processShard(shardCtx, shard, seqno, handler); err != nil {
					c.logger.Debug("ошибка обработки шарда",
						zap.Int32("workchain", shard.Workchain),
						zap.Error(err),
					)
				}
				shardCancel()
			}
		}()
	}

	// Обрабатываем мастерчейн и все шарды
	shardChan <- masterInfo
	for _, shard := range shards {
		shardChan <- shard
	}
	close(shardChan)

	wg.Wait()
	return nil
}

// processShard обрабатывает транзакции одного шарда.
func (c *IndexerClient) processShard(ctx context.Context, shard *ton.BlockIDExt, mcSeqno uint32, handler Handler) error {
	// Получаем все транзакции шарда
	var fetchedIDs []ton.TransactionShortInfo
	var after *ton.TransactionID3
	var more = true

	for more {
		ids, hasMore, err := c.api.GetBlockTransactionsV2(ctx, shard, 100, after)
		if err != nil {
			return fmt.Errorf("ошибка получения транзакций: %w", err)
		}

		fetchedIDs = append(fetchedIDs, ids...)
		more = hasMore

		if len(ids) > 0 && hasMore {
			last := ids[len(ids)-1]
			after = &ton.TransactionID3{
				Account: last.Account,
				LT:      last.LT,
			}
		}
	}

	atomic.AddInt64(&c.txTotal, int64(len(fetchedIDs)))

	// Время блока
	blockUnixtime := time.Now().Unix()

	// Обрабатываем каждую транзакцию
	for _, txInfo := range fetchedIDs {
		// Получаем полную транзакцию для анализа
		txList, err := c.api.GetTransaction(ctx, shard, address.NewAddress(0, byte(shard.Workchain), txInfo.Account), txInfo.LT)
		if err != nil {
			c.logger.Debug("не удалось получить транзакцию", zap.Error(err))
			continue
		}

		if txList == nil {
			continue
		}

		// Проверяем, является ли это деплоем
		isDeploy, codeHash := c.analyzeTransaction(txList)
		if !isDeploy {
			continue
		}

		atomic.AddInt64(&c.deploysTotal, 1)

		addrStr := fmt.Sprintf("%d:%s", shard.Workchain, hex.EncodeToString(txInfo.Account))

		event := Event{
			AccountAddress: addrStr,
			CodeHash:       codeHash,
			Timestamp:      time.Unix(int64(txList.Now), 0),
			Seqno:          mcSeqno,
			Workchain:      shard.Workchain,
			Shard:          shard.Shard,
			TxLT:           txInfo.LT,
			IsDeploy:       true,
			BlockUnixtime:  blockUnixtime,
		}

		latencyMs := time.Now().UnixMilli() - (int64(txList.Now) * 1000)
		c.recordLatency(latencyMs)

		if err := handler(event); err != nil {
			c.logger.Warn("ошибка обработчика события", zap.Error(err))
		}
	}

	return nil
}

// analyzeTransaction проверяет, является ли транзакция деплоем.
func (c *IndexerClient) analyzeTransaction(tx *tlb.Transaction) (isDeploy bool, codeHash string) {
	if tx == nil {
		return false, ""
	}

	// Проверяем входящее сообщение
	if tx.IO.In == nil {
		return false, ""
	}

	inMsg := tx.IO.In.Msg
	if inMsg == nil {
		return false, ""
	}

	// Проверяем наличие StateInit (признак деплоя)
	var stateInit *tlb.StateInit

	switch m := inMsg.(type) {
	case *tlb.InternalMessage:
		stateInit = m.StateInit
	case *tlb.ExternalMessage:
		stateInit = m.StateInit
	default:
		return false, ""
	}

	if stateInit == nil {
		return false, ""
	}

	// Есть StateInit — это деплой!
	if stateInit.Code == nil {
		return false, ""
	}

	codeHashBytes := stateInit.Code.Hash()
	codeHash = hex.EncodeToString(codeHashBytes)

	return true, codeHash
}

// recordLatency записывает latency для статистики.
func (c *IndexerClient) recordLatency(latencyMs int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.TotalEvents++
	c.stats.TotalLatencyMs += latencyMs
	c.stats.LastLatencyMs = latencyMs

	if latencyMs < c.stats.MinLatencyMs {
		c.stats.MinLatencyMs = latencyMs
	}
	if latencyMs > c.stats.MaxLatencyMs {
		c.stats.MaxLatencyMs = latencyMs
	}
}

// getAvgLatency возвращает среднюю задержку в мс.
func (c *IndexerClient) getAvgLatency() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.stats.TotalEvents == 0 {
		return 0
	}
	return c.stats.TotalLatencyMs / c.stats.TotalEvents
}

// GetStats возвращает статистику.
func (c *IndexerClient) GetStats() LatencyStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

// Catchup выгружает исторические данные.
func (c *IndexerClient) Catchup(ctx context.Context, since time.Time, handler Handler) error {
	if c.api == nil {
		return fmt.Errorf("API клиент не инициализирован")
	}

	c.logger.Info("запускаем catchup", zap.Time("since", since))

	master, err := c.api.CurrentMasterchainInfo(ctx)
	if err != nil {
		return fmt.Errorf("не удалось получить текущий мастерчейн: %w", err)
	}

	secondsAgo := time.Since(since).Seconds()
	blocksAgo := uint32(secondsAgo / 5)

	startSeqno := uint32(0)
	if master.SeqNo > blocksAgo {
		startSeqno = master.SeqNo - blocksAgo
	}

	totalBlocks := master.SeqNo - startSeqno
	c.logger.Info("catchup диапазон",
		zap.Uint32("from", startSeqno),
		zap.Uint32("to", master.SeqNo),
		zap.Uint32("total_blocks", totalBlocks),
	)

	processed := uint32(0)
	for seqno := startSeqno; seqno <= master.SeqNo; seqno++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := c.processBlock(ctx, seqno, handler); err != nil {
			c.logger.Debug("ошибка обработки блока", zap.Uint32("seqno", seqno), zap.Error(err))
		}

		processed++

		if processed%1000 == 0 {
			progress := float64(processed) / float64(totalBlocks) * 100
			c.logger.Info("catchup прогресс",
				zap.Float64("percent", progress),
				zap.Uint32("processed", processed),
			)
		}
	}

	c.logger.Info("catchup завершён", zap.Uint32("processed", processed))
	return nil
}

// RunGetMethod вызывает get-метод контракта.
func (c *IndexerClient) RunGetMethod(ctx context.Context, addrStr string, method string, args ...any) ([][]byte, error) {
	if c.api == nil {
		return nil, fmt.Errorf("API клиент не инициализирован")
	}

	addr, err := address.ParseAddr(addrStr)
	if err != nil {
		addr, err = parseRawAddress(addrStr)
		if err != nil {
			return nil, fmt.Errorf("некорректный адрес %s: %w", addrStr, err)
		}
	}

	master, err := c.api.CurrentMasterchainInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить мастерчейн: %w", err)
	}

	res, err := c.api.RunGetMethod(ctx, master, addr, method, args...)
	if err != nil {
		return nil, fmt.Errorf("ошибка вызова %s: %w", method, err)
	}

	var result [][]byte
	for i := 0; i < len(res.AsTuple()); i++ {
		val := res.AsTuple()[i]
		switch v := val.(type) {
		case []byte:
			result = append(result, v)
		case *big.Int:
			result = append(result, v.Bytes())
		case string:
			result = append(result, []byte(v))
		default:
			result = append(result, []byte(fmt.Sprintf("%v", v)))
		}
	}

	return result, nil
}

// GetCodeHash возвращает code_hash аккаунта.
func (c *IndexerClient) GetCodeHash(ctx context.Context, addrStr string) (string, error) {
	if c.api == nil {
		return "", fmt.Errorf("API клиент не инициализирован")
	}

	addr, err := address.ParseAddr(addrStr)
	if err != nil {
		addr, err = parseRawAddress(addrStr)
		if err != nil {
			return "", fmt.Errorf("некорректный адрес: %w", err)
		}
	}

	master, err := c.api.CurrentMasterchainInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("не удалось получить мастерчейн: %w", err)
	}

	acc, err := c.api.GetAccount(ctx, master, addr)
	if err != nil {
		return "", fmt.Errorf("не удалось получить аккаунт: %w", err)
	}

	if !acc.IsActive || acc.State == nil {
		return "", fmt.Errorf("аккаунт не активен")
	}

	if acc.Code == nil {
		return "", fmt.Errorf("у аккаунта нет кода")
	}

	codeHash := acc.Code.Hash()
	return hex.EncodeToString(codeHash), nil
}

// parseRawAddress парсит адрес в формате "workchain:hex".
func parseRawAddress(raw string) (*address.Address, error) {
	var workchain int32
	var hashHex string

	n, err := fmt.Sscanf(raw, "%d:%s", &workchain, &hashHex)
	if err != nil || n != 2 {
		return nil, fmt.Errorf("неверный формат адреса: %s", raw)
	}

	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil {
		return nil, fmt.Errorf("неверный hex в адресе: %w", err)
	}

	if len(hashBytes) != 32 {
		return nil, fmt.Errorf("неверная длина хэша: %d", len(hashBytes))
	}

	return address.NewAddress(0, byte(workchain), hashBytes), nil
}

// fetchGlobalConfig загружает глобальный конфиг TON
func fetchGlobalConfig(ctx context.Context, url string) (*liteclient.GlobalConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cfg liteclient.GlobalConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
