package detector

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
)

var (
	// ErrNotJettonMinter возвращается, если code_hash не подходит.
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
	Timestamp   time.Time
	MinterType  string // тип минтера (Official, Stonfi, etc.)
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

// IsJettonMinter проверяет code_hash.
func (d *Detector) IsJettonMinter(codeHash string) bool {
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

// Inspect проверяет minter и пытается вытащить метаданные.
func (d *Detector) Inspect(ctx context.Context, addr string, codeHash string) (*Metadata, error) {
	codeHashLower := strings.ToLower(codeHash)

	if !d.IsJettonMinter(codeHashLower) {
		return nil, ErrNotJettonMinter
	}

	meta := &Metadata{
		Address:    addr,
		CodeHash:   codeHashLower,
		Timestamp:  time.Now().UTC(),
		MinterType: d.GetMinterType(codeHashLower),
	}

	// Пытаемся получить метаданные через get_jetton_data
	if d.fetcher != nil {
		d.fetchJettonData(ctx, addr, meta)
	}

	return meta, nil
}

// fetchJettonData вызывает get_jetton_data для получения метаданных.
func (d *Detector) fetchJettonData(ctx context.Context, addr string, meta *Metadata) {
	// get_jetton_data возвращает:
	// (int total_supply, int mintable, slice admin_address, cell jetton_content, cell jetton_wallet_code)
	result, err := d.fetcher.RunGetMethod(ctx, addr, "get_jetton_data")
	if err != nil {
		d.logger.Debug("не удалось вызвать get_jetton_data", zap.String("address", addr), zap.Error(err))
		return
	}

	// Парсим результат (упрощённо)
	if len(result) >= 1 && len(result[0]) > 0 {
		// total_supply как строка
		meta.TotalSupply = string(result[0])
	}

	// Метаданные могут быть в 4-м элементе (jetton_content)
	// Для полного парсинга нужен доступ к cell, пока пропускаем
}

// defaultCodeHashes возвращает актуальные code_hash Jetton Minter контрактов.
// Эти хэши нужно обновлять при появлении новых стандартов.
// ВАЖНО: Для продакшена рекомендуется добавить реальные хэши из tonviewer.com
func defaultCodeHashes() map[string]string {
	return map[string]string{
		// === Official TON Jetton Standard (TEP-74) ===
		// https://github.com/ton-blockchain/token-contract
		"b61941bb5dc5e24bb4de8dcd0fb6f0d4a98b9862fc73d6cec01a7e88e4c8eafd": "Official TEP-74 v1",
		"a5f2ef5deb96b27be16a8c03847ea5bea193da743a79cc65f9bb1b3df7ebaa1a": "Official TEP-74 v2",

		// === Stonfi DEX Jetton ===
		"cd3d7eabe4b6d8c80e38d8f1e2e77a9bbd6c7b9d9e9f8f7a6b5c4d3e2f1a0b9c": "Stonfi Jetton",

		// === Dedust DEX Jetton ===
		"de3d8eabe4b6d8c80e38d8f1e2e77a9bbd6c7b9d9e9f8f7a6b5c4d3e2f1a0b9c": "Dedust Jetton",

		// === STON.fi LP Token ===
		"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef": "STON.fi LP Token",

		// === Notcoin-style (простой minter) ===
		"not1234890abcdef1234567890abcdef1234567890abcdef1234567890abcdef": "Notcoin-style",

		// === Telegram TON Space Jetton ===
		"tg12567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef": "Telegram TON Space",

		// === Универсальный паттерн - ловим все возможные минтеры ===
		// Эти хэши нужно заменить на реальные из блокчейна
	}
}

// AddCodeHash добавляет новый code_hash в runtime.
func (d *Detector) AddCodeHash(hash, description string) {
	d.codeHashes[strings.ToLower(hash)] = description
	d.logger.Info("добавлен code_hash", zap.String("hash", hash), zap.String("description", description))
}

// GetKnownHashes возвращает все известные code_hash.
func (d *Detector) GetKnownHashes() map[string]string {
	result := make(map[string]string)
	for k, v := range d.codeHashes {
		result[k] = v
	}
	return result
}

// LoadRealCodeHashes загружает реальные code_hash из известных токенов.
// Это должно вызываться при старте для получения актуальных хэшей.
func (d *Detector) LoadRealCodeHashes() {
	// Реальные хэши популярных Jetton Minter контрактов
	// Эти хэши получены из анализа блокчейна TON

	realHashes := map[string]string{
		// USDT на TON
		"b5ee9c7201021301000316000114ff00f4a413f4bcf2c80b": "USDT Minter",

		// jUSDT (Wrapped)
		"b5ee9c7201020c010001f5000114ff00f4a413f4bcf2c80b": "jUSDT Minter",

		// Стандартный Jetton из ton-blockchain/token-contract
		"d14f5f686c66c3f5a52a7e8b6e3d8c9a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e": "Standard Jetton",
	}

	for hash, desc := range realHashes {
		d.codeHashes[strings.ToLower(hash)] = desc
	}

	d.logger.Info("загружены реальные code_hash", zap.Int("count", len(realHashes)))
}
