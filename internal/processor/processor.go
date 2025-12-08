package processor

import (
	"context"
	"time"

	"github.com/yourname/hyper-sniper-indexer/internal/detector"
	"github.com/yourname/hyper-sniper-indexer/internal/storage"
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

// Processor отвечает за обработку событий из ton-indexer.
type Processor struct {
	detector *detector.Detector
	client   ton.Client
	cache    *storage.RedisCache
	logger   *zap.Logger
}

// NewProcessor создаёт обработчик.
func NewProcessor(det *detector.Detector, client ton.Client, cache *storage.RedisCache, logger *zap.Logger) *Processor {
	return &Processor{
		detector: det,
		client:   client,
		cache:    cache,
		logger:   logger,
	}
}

// Handle обрабатывает единичное событие из ton-indexer.
func (p *Processor) Handle(event ton.Event) error {
	if !event.IsDeploy {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if p.cache != nil && event.Seqno > 0 {
		isNew, err := p.cache.RegisterSeqno(ctx, event.Seqno)
		if err != nil {
			p.logger.Warn("ошибка записи seqno", zap.Error(err))
		}
		if !isNew {
			return nil
		}
	}

	if p.cache != nil {
		seen, err := p.cache.IsMinterKnown(ctx, event.AccountAddress)
		if err != nil {
			p.logger.Warn("ошибка проверки минтера в кэше", zap.Error(err))
		}
		if seen {
			return nil
		}
	}

	codeHash := event.CodeHash
	if codeHash == "" {
		ch, err := p.client.GetCodeHash(ctx, event.AccountAddress)
		if err != nil {
			p.logger.Warn("не удалось получить code_hash", zap.String("address", event.AccountAddress), zap.Error(err))
			return nil
		}
		codeHash = ch
	}

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

	if p.cache != nil {
		if err := p.cache.RememberMinter(ctx, meta.Address); err != nil {
			p.logger.Warn("не удалось сохранить минтер в кэш", zap.Error(err))
		}
	}

	return nil
}

