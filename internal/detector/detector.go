package detector

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
)

var (
	// ErrNotJettonMinter возвращается, если контракт не прошёл верификацию.
	ErrNotJettonMinter = errors.New("не JettonMinter")
)

// Metadata описывает основные поля JettonMinter.
type Metadata struct {
	Address     string
	CodeHash    string
	Name        string
	Symbol      string
	Decimals    int
	TotalSupply string
	ContentURI  string // URI метаданных (offchain)
	AdminAddr   string // адрес админа
	Mintable    bool   // можно ли минтить ещё
	Timestamp   time.Time
	MinterType  string // тип минтера (Official, Stonfi, etc.)

	// Флаги верификации
	VerifiedByInterface bool // прошёл проверку по get-методам
	KnownCodeHash       bool // code_hash в whitelist

	// Latency
	DetectionLatencyMs int64
}

// MetadataFetcher интерфейс для получения метаданных (реализуется TON клиентом).
type MetadataFetcher interface {
	RunGetMethod(ctx context.Context, address string, method string, args ...any) ([][]byte, error)
}

// Detector проверяет code_hash и достаёт метаданные.
type Detector struct {
	codeHashes map[string]string // hash -> description
	fetcher    MetadataFetcher
	logger     *zap.Logger
}

// NewDetector создаёт детектор с заранее известными code_hash.
func NewDetector(fetcher MetadataFetcher, logger *zap.Logger) *Detector {
	hashes := make(map[string]string)
	for hash, desc := range defaultCodeHashes() {
		hashes[strings.ToLower(hash)] = desc
	}

	return &Detector{
		codeHashes: hashes,
		fetcher:    fetcher,
		logger:     logger,
	}
}

// IsKnownCodeHash проверяет, есть ли code_hash в whitelist.
func (d *Detector) IsKnownCodeHash(codeHash string) bool {
	_, ok := d.codeHashes[strings.ToLower(codeHash)]
	return ok
}

// GetMinterType возвращает описание типа минтера по code_hash.
func (d *Detector) GetMinterType(codeHash string) string {
	if desc, ok := d.codeHashes[strings.ToLower(codeHash)]; ok {
		return desc
	}
	return "Unknown"
}

// VerifyAndInspect проверяет контракт по интерфейсу TEP-74 и извлекает метаданные.
// КЛЮЧЕВОЙ МЕТОД: Проверяет наличие get_jetton_data и get_wallet_address.
// Это позволяет обнаруживать ЛЮБЫЕ Jetton Minter, даже с неизвестным code_hash.
func (d *Detector) VerifyAndInspect(ctx context.Context, addr string, codeHash string) (*Metadata, error) {
	startTime := time.Now()
	codeHashLower := strings.ToLower(codeHash)

	meta := &Metadata{
		Address:       addr,
		CodeHash:      codeHashLower,
		Timestamp:     time.Now().UTC(),
		KnownCodeHash: d.IsKnownCodeHash(codeHashLower),
		MinterType:    d.GetMinterType(codeHashLower),
	}

	// Если code_hash неизвестен, но fetcher доступен — проверяем по интерфейсу
	if d.fetcher != nil {
		verified, jettonData := d.verifyJettonInterface(ctx, addr)
		meta.VerifiedByInterface = verified

		if verified {
			// Заполняем метаданные из get_jetton_data
			if jettonData != nil {
				meta.TotalSupply = jettonData.TotalSupply
				meta.Mintable = jettonData.Mintable
				meta.AdminAddr = jettonData.AdminAddr
				meta.ContentURI = jettonData.ContentURI
				meta.Name = jettonData.Name
				meta.Symbol = jettonData.Symbol
				meta.Decimals = jettonData.Decimals
			}

			// Если code_hash неизвестен, но интерфейс прошёл — помечаем как новый тип
			if !meta.KnownCodeHash {
				meta.MinterType = "Interface-Verified (Unknown Code)"
				d.logger.Info("обнаружен новый тип Jetton Minter",
					zap.String("address", addr),
					zap.String("code_hash", codeHashLower),
				)
			}
		}
	}

	// Решаем, является ли это Jetton Minter
	if !meta.KnownCodeHash && !meta.VerifiedByInterface {
		return nil, ErrNotJettonMinter
	}

	meta.DetectionLatencyMs = time.Since(startTime).Milliseconds()
	return meta, nil
}

// JettonData структура для данных из get_jetton_data.
type JettonData struct {
	TotalSupply string
	Mintable    bool
	AdminAddr   string
	ContentURI  string
	Name        string
	Symbol      string
	Decimals    int
}

// verifyJettonInterface проверяет контракт по интерфейсу TEP-74.
// Вызывает get_jetton_data и проверяет формат ответа.
func (d *Detector) verifyJettonInterface(ctx context.Context, addr string) (bool, *JettonData) {
	// Таймаут на проверку интерфейса
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// 1. Проверяем get_jetton_data
	// По TEP-74 должен вернуть:
	// (int total_supply, int mintable, slice admin_address, cell jetton_content, cell jetton_wallet_code)
	result, err := d.fetcher.RunGetMethod(checkCtx, addr, "get_jetton_data")
	if err != nil {
		d.logger.Debug("get_jetton_data не доступен",
			zap.String("address", addr),
			zap.Error(err),
		)
		return false, nil
	}

	// Проверяем, что вернулось хотя бы 4 элемента (минимум для TEP-74)
	if len(result) < 4 {
		d.logger.Debug("get_jetton_data вернул мало данных",
			zap.String("address", addr),
			zap.Int("result_len", len(result)),
		)
		return false, nil
	}

	// Парсим результат
	data := &JettonData{}

	// total_supply (первый элемент)
	if len(result[0]) > 0 {
		data.TotalSupply = bytesToBigIntString(result[0])
	}

	// mintable (второй элемент, обычно -1 или 0)
	if len(result) > 1 && len(result[1]) > 0 {
		// В TON true = -1, false = 0
		data.Mintable = !isZeroBytes(result[1])
	}

	// admin_address (третий элемент)
	if len(result) > 2 && len(result[2]) > 0 {
		data.AdminAddr = parseAddressFromBytes(result[2])
	}

	// content (четвёртый элемент) — может быть URI или on-chain данные
	if len(result) > 3 && len(result[3]) > 0 {
		data.ContentURI = extractContentURI(result[3])
		// Пытаемся получить name/symbol из content
		name, symbol, decimals := parseJettonContent(result[3])
		data.Name = name
		data.Symbol = symbol
		data.Decimals = decimals
	}

	d.logger.Debug("get_jetton_data успешно",
		zap.String("address", addr),
		zap.String("total_supply", data.TotalSupply),
		zap.Bool("mintable", data.Mintable),
		zap.String("admin", data.AdminAddr),
	)

	return true, data
}

// bytesToBigIntString конвертирует байты в строку числа.
func bytesToBigIntString(b []byte) string {
	if len(b) == 0 {
		return "0"
	}
	// Простая конвертация для отображения
	// В продакшене нужен полноценный BigInt
	return string(b)
}

// isZeroBytes проверяет, все ли байты нулевые.
func isZeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// parseAddressFromBytes пытается распарсить адрес из байтов.
func parseAddressFromBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// Упрощённая версия — в продакшене нужен полный парсинг slice
	return string(b)
}

// extractContentURI извлекает URI из content cell.
func extractContentURI(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	// Первый байт — тип контента
	// 0x00 = on-chain
	// 0x01 = off-chain (URI)
	if len(b) > 1 && b[0] == 0x01 {
		// Off-chain — остальные байты это URI
		return strings.TrimSpace(string(b[1:]))
	}

	return ""
}

// parseJettonContent пытается извлечь name/symbol из content.
func parseJettonContent(b []byte) (name, symbol string, decimals int) {
	if len(b) == 0 {
		return "", "", 9 // default decimals
	}

	// Упрощённый парсинг
	// В продакшене нужен полноценный парсинг Snake format или JSON
	content := string(b)

	// Пытаемся найти name и symbol в строке
	if idx := strings.Index(content, "name"); idx != -1 {
		name = extractValue(content[idx:], "name")
	}
	if idx := strings.Index(content, "symbol"); idx != -1 {
		symbol = extractValue(content[idx:], "symbol")
	}

	return name, symbol, 9 // default decimals = 9 для TON
}

// extractValue извлекает значение поля из строки.
func extractValue(s, field string) string {
	// Очень простой парсинг
	start := strings.Index(s, ":")
	if start == -1 {
		return ""
	}

	rest := strings.TrimSpace(s[start+1:])
	if len(rest) == 0 {
		return ""
	}

	// Если в кавычках
	if rest[0] == '"' {
		end := strings.Index(rest[1:], "\"")
		if end != -1 {
			return rest[1 : end+1]
		}
	}

	// До запятой или конца
	end := strings.IndexAny(rest, ",}")
	if end == -1 {
		end = len(rest)
	}

	return strings.TrimSpace(rest[:end])
}

// defaultCodeHashes возвращает известные code_hash Jetton Minter контрактов.
// ВАЖНО: Эти хэши нужно регулярно обновлять и дополнять.
func defaultCodeHashes() map[string]string {
	return map[string]string{
		// === Official TON Jetton Standard (TEP-74) ===
		// Из ton-blockchain/token-contract
		// Эти хэши вычисляются из скомпилированного кода

		// Jetton Minter из официального репозитория TON
		// https://github.com/ton-blockchain/token-contract
		"b5ee9c7241010101001000": "Official Jetton Minter (short)",

		// === Stonfi DEX ===
		// Популярный DEX на TON
		"stonfi_jetton_minter_v1": "Stonfi Jetton v1",
		"stonfi_jetton_minter_v2": "Stonfi Jetton v2",

		// === DeDust DEX ===
		"dedust_jetton_minter": "DeDust Jetton",

		// === USDT на TON ===
		// Централизованный стейблкоин
		"usdt_ton_minter": "USDT Minter",

		// === Notcoin ===
		"notcoin_minter": "Notcoin Minter",

		// PLACEHOLDER: Нужно заменить на реальные хэши!
		// Для получения реальных хэшей:
		// 1. Найти контракт на tonviewer.com
		// 2. Посмотреть code_hash в информации об аккаунте
		// 3. Добавить сюда
	}
}

// AddCodeHash добавляет новый code_hash в runtime.
func (d *Detector) AddCodeHash(hash, description string) {
	d.codeHashes[strings.ToLower(hash)] = description
	d.logger.Info("добавлен code_hash",
		zap.String("hash", hash[:16]+"..."),
		zap.String("description", description),
	)
}

// GetKnownHashes возвращает все известные code_hash.
func (d *Detector) GetKnownHashes() map[string]string {
	result := make(map[string]string)
	for k, v := range d.codeHashes {
		result[k] = v
	}
	return result
}

// LoadRealCodeHashes загружает реальные code_hash.
// В продакшене это должно подтягиваться из внешних источников.
func (d *Detector) LoadRealCodeHashes() {
	// TODO: Загрузка из:
	// 1. TonAPI
	// 2. Известных DEX (Stonfi, DeDust)
	// 3. Локальной базы проверенных хэшей

	d.logger.Info("code_hash загружены",
		zap.Int("total_hashes", len(d.codeHashes)),
	)
}
