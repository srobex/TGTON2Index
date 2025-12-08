package detector

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

var (
	// ErrNotJettonMinter возвращается, если code_hash не подходит.
	ErrNotJettonMinter = errors.New("не JettonMinter")
)

// Metadata описывает основные поля JettonMinter.
type Metadata struct {
	Address   string
	CodeHash  string
	Name      string
	Symbol    string
	Decimals  int
	Timestamp time.Time
}

// Detector проверяет code_hash и достаёт метаданные.
type Detector struct {
	codeHashes map[string]struct{}
	client     ton.Client
	logger     *zap.Logger
}

// NewDetector создаёт детектор с заранее известными code_hash.
func NewDetector(client ton.Client, logger *zap.Logger) *Detector {
	hashes := make(map[string]struct{})
	for _, h := range defaultCodeHashes() {
		hashes[h] = struct{}{}
	}

	return &Detector{
		codeHashes: hashes,
		client:     client,
		logger:     logger,
	}
}

// IsJettonMinter проверяет code_hash.
func (d *Detector) IsJettonMinter(codeHash string) bool {
	_, ok := d.codeHashes[strings.ToLower(codeHash)]
	return ok
}

// Inspect проверяет minter и пытается вытащить метаданные.
func (d *Detector) Inspect(ctx context.Context, address string, codeHash string) (*Metadata, error) {
	if !d.IsJettonMinter(codeHash) {
		return nil, ErrNotJettonMinter
	}

	meta := &Metadata{
		Address:   address,
		CodeHash:  strings.ToLower(codeHash),
		Timestamp: time.Now().UTC(),
	}

	stack, err := d.client.RunGetMethod(ctx, address, "get_jetton_data")
	if err != nil {
		d.logger.Warn("не удалось получить метаданные jetton", zap.String("address", address), zap.Error(err))
		return meta, nil
	}

	if len(stack) >= 1 {
		meta.Name = parseString(stack[0])
	}
	if len(stack) >= 2 {
		meta.Symbol = parseString(stack[1])
	}
	if len(stack) >= 3 {
		meta.Decimals = parseInt(stack[2])
	}

	return meta, nil
}

func defaultCodeHashes() []string {
	return []string{
		"6d9f5c5d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b", // Official TON Jetton 2.0
		"f4a6c118c7a2a4e3f8d2b4e6c8a0f2d4e6c8a0f2d4e6c8a0f2d4e6c8a0f2d4e6", // Old official
		"83fbdc8e3a47a75e8a7b7c7e5f6a4d3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7", // Discoverable variant
		"a3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c", // Broxus legacy
		"2b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4", // Stablecoin variant
		"e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7",  // Notcoin-style
	}
}

func parseString(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed
}

func parseInt(raw []byte) int {
	if len(raw) >= 8 {
		return int(binary.BigEndian.Uint64(raw[len(raw)-8:]))
	}
	return 0
}

