package ton

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"go.uber.org/zap"
)

const (
	// URL глобального конфига TON
	mainnetConfigURL = "https://ton.org/global-config.json"
	testnetConfigURL = "https://ton.org/testnet-global.config.json"

	// Интервал опроса новых блоков (чем меньше — тем быстрее, но больше нагрузка)
	blockPollInterval = 100 * time.Millisecond

	// Таймаут на получение блока
	blockTimeout = 3 * time.Second

	// Количество воркеров для параллельной обработки шардов
	shardWorkers = 8
)

// Event описывает минимальный набор данных о транзакции с деплоем.
type Event struct {
	AccountAddress string
	CodeHash       string
	Timestamp      time.Time
	Seqno          uint32
	Workchain      int32
	Shard          int64
	IsDeploy       bool
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

// IndexerClient — высокоскоростной клиент для индексации TON.
type IndexerClient struct {
	network     string
	liteservers []string
	logger      *zap.Logger

	pool   *liteclient.ConnectionPool
	api    ton.APIClientWrapped
	mu     sync.RWMutex
	lastMC uint32 // последний обработанный seqno мастерчейна
}

// GetAPI возвращает API клиент для прямого доступа (нужен детектору).
func (c *IndexerClient) GetAPI() ton.APIClientWrapped {
	return c.api
}

// NewIndexerClient создаёт клиента для выбранной сети.
func NewIndexerClient(network string, liteservers []string, logger *zap.Logger) *IndexerClient {
	return &IndexerClient{
		network:     network,
		liteservers: liteservers,
		logger:      logger,
	}
}

// Start подключается к liteserver'ам и подготавливает API.
func (c *IndexerClient) Start(ctx context.Context) error {
	c.pool = liteclient.NewConnectionPool()

	// Определяем URL конфига
	configURL := mainnetConfigURL
	if c.network == "testnet" {
		configURL = testnetConfigURL
	}

	// Если liteservers не указаны — загружаем из глобального конфига
	if len(c.liteservers) == 0 {
		c.logger.Info("загружаем liteservers из глобального конфига", zap.String("url", configURL))

		cfg, err := fetchGlobalConfig(ctx, configURL)
		if err != nil {
			return fmt.Errorf("не удалось загрузить глобальный конфиг: %w", err)
		}

		if err := c.pool.AddConnectionsFromConfig(ctx, cfg); err != nil {
			return fmt.Errorf("не удалось подключиться к liteservers: %w", err)
		}
	} else {
		// Используем указанные вручную liteservers
		c.logger.Info("используем указанные liteservers", zap.Int("count", len(c.liteservers)))

		cfg, err := fetchGlobalConfig(ctx, configURL)
		if err != nil {
			return fmt.Errorf("не удалось загрузить глобальный конфиг: %w", err)
		}

		if err := c.pool.AddConnectionsFromConfig(ctx, cfg); err != nil {
			return fmt.Errorf("не удалось подключиться к liteservers: %w", err)
		}
	}

	// Создаём API клиент с повторными попытками
	c.api = ton.NewAPIClient(c.pool, ton.ProofCheckPolicyFast).WithRetry()

	c.logger.Info("подключение к TON установлено", zap.String("network", c.network))
	return nil
}

// Subscribe подключается к потоку новых блоков и транзакций в реальном времени.
// Это основной метод для высокоскоростной индексации.
func (c *IndexerClient) Subscribe(ctx context.Context, handler Handler) error {
	if c.api == nil {
		return fmt.Errorf("API клиент не инициализирован, вызовите Start() сначала")
	}

	c.logger.Info("запускаем подписку на новые блоки")

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

			// Обрабатываем все пропущенные блоки (на случай если пропустили несколько)
			for seqno := lastSeqno + 1; seqno <= newMaster.SeqNo; seqno++ {
				if err := c.processBlock(ctx, seqno, handler); err != nil {
					c.logger.Warn("ошибка обработки блока", zap.Uint32("seqno", seqno), zap.Error(err))
				}
			}

			c.mu.Lock()
			c.lastMC = newMaster.SeqNo
			c.mu.Unlock()

			latency := time.Since(startTime)
			c.logger.Debug("блок обработан",
				zap.Uint32("seqno", newMaster.SeqNo),
				zap.Duration("latency", latency),
			)
		}

		// Минимальная задержка между опросами
		time.Sleep(blockPollInterval)
	}
}

// processBlock обрабатывает один блок мастерчейна и все его шарды.
func (c *IndexerClient) processBlock(ctx context.Context, seqno uint32, handler Handler) error {
	blockCtx, cancel := context.WithTimeout(ctx, blockTimeout)
	defer cancel()

	// Получаем информацию о блоке мастерчейна
	masterInfo, err := c.api.LookupBlock(blockCtx, -1, 0x8000000000000000, seqno)
	if err != nil {
		return fmt.Errorf("не удалось найти блок %d: %w", seqno, err)
	}

	// Получаем все шарды этого блока
	shards, err := c.api.GetBlockShardsInfo(blockCtx, masterInfo)
	if err != nil {
		return fmt.Errorf("не удалось получить шарды блока %d: %w", seqno, err)
	}

	// Параллельная обработка шардов для максимальной скорости
	var wg sync.WaitGroup
	shardChan := make(chan *ton.BlockIDExt, len(shards))

	// Запускаем воркеры
	for i := 0; i < shardWorkers && i < len(shards); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shard := range shardChan {
				if err := c.processShard(ctx, shard, seqno, handler); err != nil {
					c.logger.Debug("ошибка обработки шарда",
						zap.Int32("workchain", shard.Workchain),
						zap.Error(err),
					)
				}
			}
		}()
	}

	// Отправляем шарды на обработку
	for _, shard := range shards {
		shardChan <- shard
	}
	close(shardChan)

	wg.Wait()
	return nil
}

// processShard обрабатывает транзакции одного шарда.
func (c *IndexerClient) processShard(ctx context.Context, shard *ton.BlockIDExt, mcSeqno uint32, handler Handler) error {
	shardCtx, cancel := context.WithTimeout(ctx, blockTimeout)
	defer cancel()

	// Получаем все транзакции шарда
	var txList []*tlb.Transaction
	var after *ton.TransactionID3
	var more = true

	for more {
		txs, err := c.api.GetBlockTransactionsV2(shardCtx, shard, 100, after)
		if err != nil {
			return fmt.Errorf("ошибка получения транзакций: %w", err)
		}

		txList = append(txList, txs...)

		if len(txs) < 100 {
			more = false
		} else {
			lastTx := txs[len(txs)-1]
			after = &ton.TransactionID3{
				Account: lastTx.AccountAddr,
				LT:      lastTx.LT,
			}
		}
	}

	// Обрабатываем каждую транзакцию
	for _, tx := range txList {
		// Проверяем, является ли это деплоем контракта
		isDeploy := c.isContractDeploy(tx)
		if !isDeploy {
			continue
		}

		addr := tx.AccountAddr
		addrStr := fmt.Sprintf("%d:%s", shard.Workchain, hex.EncodeToString(addr))

		// Получаем code_hash нового контракта
		codeHash, err := c.getCodeHashFromTx(tx)
		if err != nil {
			c.logger.Debug("не удалось получить code_hash из транзакции", zap.Error(err))
			continue
		}

		event := Event{
			AccountAddress: addrStr,
			CodeHash:       codeHash,
			Timestamp:      time.Unix(int64(tx.Now), 0),
			Seqno:          mcSeqno,
			Workchain:      shard.Workchain,
			Shard:          int64(shard.Shard),
			IsDeploy:       true,
		}

		if err := handler(event); err != nil {
			c.logger.Warn("ошибка обработчика события", zap.Error(err))
		}
	}

	return nil
}

// isContractDeploy проверяет, является ли транзакция деплоем нового контракта.
func (c *IndexerClient) isContractDeploy(tx *tlb.Transaction) bool {
	// Деплой — это когда аккаунт переходит из uninit в active
	// и в транзакции есть StateInit с кодом

	if tx.StateUpdate == nil {
		return false
	}

	// Проверяем, что аккаунт был неактивен до транзакции
	oldState := tx.StateUpdate.OldHash
	newState := tx.StateUpdate.NewHash

	// Если хэши состояния изменились и есть входящее сообщение с StateInit
	if oldState == newState {
		return false
	}

	// Проверяем входящее сообщение
	if tx.IO.In == nil {
		return false
	}

	inMsg := tx.IO.In.Msg
	if inMsg == nil {
		return false
	}

	// Проверяем наличие StateInit (признак деплоя)
	switch m := inMsg.(type) {
	case *tlb.InternalMessage:
		return m.StateInit != nil
	case *tlb.ExternalMessage:
		return m.StateInit != nil
	}

	return false
}

// getCodeHashFromTx извлекает code_hash из транзакции деплоя.
func (c *IndexerClient) getCodeHashFromTx(tx *tlb.Transaction) (string, error) {
	if tx.IO.In == nil || tx.IO.In.Msg == nil {
		return "", fmt.Errorf("нет входящего сообщения")
	}

	var stateInit *tlb.StateInit

	switch m := tx.IO.In.Msg.(type) {
	case *tlb.InternalMessage:
		stateInit = m.StateInit
	case *tlb.ExternalMessage:
		stateInit = m.StateInit
	}

	if stateInit == nil || stateInit.Code == nil {
		return "", fmt.Errorf("нет StateInit или Code")
	}

	// Вычисляем хэш кода
	codeHash := stateInit.Code.Hash()
	return hex.EncodeToString(codeHash), nil
}

// Catchup выгружает исторические данные с указанного момента.
func (c *IndexerClient) Catchup(ctx context.Context, since time.Time, handler Handler) error {
	if c.api == nil {
		return fmt.Errorf("API клиент не инициализирован")
	}

	c.logger.Info("запускаем catchup", zap.Time("since", since))

	// Получаем текущий блок
	master, err := c.api.CurrentMasterchainInfo(ctx)
	if err != nil {
		return fmt.Errorf("не удалось получить текущий мастерчейн: %w", err)
	}

	// Примерно вычисляем стартовый seqno (1 блок ~ 5 секунд)
	secondsAgo := time.Since(since).Seconds()
	blocksAgo := uint32(secondsAgo / 5)

	startSeqno := uint32(0)
	if master.SeqNo > blocksAgo {
		startSeqno = master.SeqNo - blocksAgo
	}

	c.logger.Info("catchup диапазон",
		zap.Uint32("from", startSeqno),
		zap.Uint32("to", master.SeqNo),
		zap.Uint32("blocks", master.SeqNo-startSeqno),
	)

	// Обрабатываем блоки
	for seqno := startSeqno; seqno <= master.SeqNo; seqno++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := c.processBlock(ctx, seqno, handler); err != nil {
			c.logger.Debug("ошибка обработки исторического блока", zap.Uint32("seqno", seqno), zap.Error(err))
		}

		// Логируем прогресс каждые 1000 блоков
		if seqno%1000 == 0 {
			progress := float64(seqno-startSeqno) / float64(master.SeqNo-startSeqno) * 100
			c.logger.Info("catchup прогресс", zap.Float64("percent", progress), zap.Uint32("current", seqno))
		}
	}

	c.logger.Info("catchup завершён")
	return nil
}

// RunGetMethod вызывает get-метод контракта.
func (c *IndexerClient) RunGetMethod(ctx context.Context, address string, method string, args ...any) ([][]byte, error) {
	if c.api == nil {
		return nil, fmt.Errorf("API клиент не инициализирован")
	}

	addr, err := ton.ParseAddress(address)
	if err != nil {
		// Пробуем распарсить raw адрес
		return nil, fmt.Errorf("некорректный адрес: %w", err)
	}

	master, err := c.api.CurrentMasterchainInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить мастерчейн: %w", err)
	}

	res, err := c.api.RunGetMethod(ctx, master, addr, method, args...)
	if err != nil {
		return nil, fmt.Errorf("ошибка вызова %s: %w", method, err)
	}

	// Конвертируем результат в байты
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
func (c *IndexerClient) GetCodeHash(ctx context.Context, address string) (string, error) {
	if c.api == nil {
		return "", fmt.Errorf("API клиент не инициализирован")
	}

	addr, err := ton.ParseAddress(address)
	if err != nil {
		return "", fmt.Errorf("некорректный адрес: %w", err)
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

	// Получаем код контракта и вычисляем хэш
	if acc.Code == nil {
		return "", fmt.Errorf("у аккаунта нет кода")
	}

	codeHash := acc.Code.Hash()
	return hex.EncodeToString(codeHash), nil
}

// GlobalConfig структура для парсинга глобального конфига TON
type GlobalConfig struct {
	Liteservers []LiteserverConfig `json:"liteservers"`
}

type LiteserverConfig struct {
	IP   int64  `json:"ip"`
	Port int    `json:"port"`
	ID   IDKey  `json:"id"`
}

type IDKey struct {
	Type string `json:"@type"`
	Key  string `json:"key"`
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

// Вспомогательные функции для работы с ключами
func base64ToEd25519(b64 string) (ed25519.PublicKey, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("неверный размер ключа")
	}
	return ed25519.PublicKey(data), nil
}
