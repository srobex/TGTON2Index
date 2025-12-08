package processor

import (
	"context"
	"time"

	"github.com/yourname/hyper-sniper-indexer/internal/detector"
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

// Processor отвечает за обработку событий из ton-indexer.
type Processor struct {
	detector *detector.Detector
	client   ton.Client
	logger   *zap.Logger
}

// NewProcessor создаёт обработчик.
func NewProcessor(det *detector.Detector, client ton.Client, logger *zap.Logger) *Processor {
	return &Processor{
		detector: det,
		client:   client,
		logger:   logger,
	}
}

// Handle обрабатывает единичное событие из ton-indexer.
func (p *Processor) Handle(event ton.Event) error {
	if !event.IsDeploy {
		return nil
	}

	codeHash := event.CodeHash
	if codeHash == "" {
		ch, err := p.client.GetCodeHash(context.Background(), event.AccountAddress)
		if err != nil {
			p.logger.Warn("не удалось получить code_hash", zap.String("address", event.AccountAddress), zap.Error(err))
			return nil
		}
		codeHash = ch
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	meta, err := p.detector.Inspect(ctx, event.AccountAddress, codeHash)
	if err != nil {
		if err == detector.ErrNotJettonMinter {
			return nil
		}
		p.logger.Warn("ошибка детекции минтера", zap.String("address", event.AccountAddress), zap.Error(err))
		return nil
	}

	p.logger.Info(
		"найден новый JettonMinter",
		zap.String("address", meta.Address),
		zap.String("code_hash", meta.CodeHash),
		zap.String("name", meta.Name),
		zap.String("symbol", meta.Symbol),
		zap.Int("decimals", meta.Decimals),
	)

	return nil
}

