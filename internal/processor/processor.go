package processor

import (
	"context"
	"time"

	"github.com/yourname/hyper-sniper-indexer/internal/detector"
	"github.com/yourname/hyper-sniper-indexer/internal/notifier"
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

// Processor отвечает за обработку событий из ton-indexer.
type Processor struct {
	detector *detector.Detector
	client   ton.Client
	cache    Cache
	notifier *notifier.Notifier
	logger   *zap.Logger
}

// Cache описывает минимальный интерфейс антидублирования.
type Cache interface {
	RegisterSeqno(ctx context.Context, seqno uint32) (bool, error)
	IsMinterKnown(ctx context.Context, address string) (bool, error)
	RememberMinter(ctx context.Context, address string) error
}

// NewProcessor создаёт обработчик.
func NewProcessor(det *detector.Detector, client ton.Client, cache Cache, ntf *notifier.Notifier, logger *zap.Logger) *Processor {
	return &Processor{
		detector: det,
		client:   client,
		cache:    cache,
		notifier: ntf,
		logger:   logger,
	}
}

// Handle обрабатывает единичное событие из ton-indexer.
func (p *Processor) Handle(event ton.Event) error {
	// Пропускаем если это не деплой
	if !event.IsDeploy {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Проверяем дубликаты по seqno
	if p.cache != nil && event.Seqno > 0 {
		isNew, err := p.cache.RegisterSeqno(ctx, event.Seqno)
		if err != nil {
			p.logger.Warn("ошибка записи seqno", zap.Error(err))
		}
		if !isNew {
			return nil
		}
	}

	// Проверяем, не обрабатывали ли мы этот адрес
	if p.cache != nil {
		seen, err := p.cache.IsMinterKnown(ctx, event.AccountAddress)
		if err != nil {
			p.logger.Warn("ошибка проверки минтера в кэше", zap.Error(err))
		}
		if seen {
			return nil
		}
	}

	// Получаем code_hash (если не пришёл в событии)
	codeHash := event.CodeHash
	if codeHash == "" {
		ch, err := p.client.GetCodeHash(ctx, event.AccountAddress)
		if err != nil {
			p.logger.Debug("не удалось получить code_hash",
				zap.String("address", event.AccountAddress),
				zap.Error(err),
			)
			return nil
		}
		codeHash = ch
	}

	// Проверяем, является ли это JettonMinter
	if !p.detector.IsJettonMinter(codeHash) {
		return nil
	}

	// Это JettonMinter! Получаем метаданные
	meta, err := p.detector.Inspect(ctx, event.AccountAddress, codeHash)
	if err != nil {
		if err == detector.ErrNotJettonMinter {
			return nil
		}
		p.logger.Warn("ошибка детекции минтера",
			zap.String("address", event.AccountAddress),
			zap.Error(err),
		)
		return nil
	}

	// Вычисляем задержку обнаружения
	detectionLatency := time.Since(event.Timestamp)

	// Логируем находку
	p.logger.Info(
		"🚀 НАЙДЕН НОВЫЙ JETTON MINTER",
		zap.String("address", meta.Address),
		zap.String("code_hash", meta.CodeHash),
		zap.String("name", meta.Name),
		zap.String("symbol", meta.Symbol),
		zap.String("type", p.detector.GetMinterType(meta.CodeHash)),
		zap.Duration("detection_latency", detectionLatency),
		zap.Int32("workchain", event.Workchain),
	)

	// Запоминаем адрес в кэше
	if p.cache != nil {
		if err := p.cache.RememberMinter(ctx, meta.Address); err != nil {
			p.logger.Warn("не удалось сохранить минтер в кэш", zap.Error(err))
		}
	}

	// Отправляем уведомления
	if p.notifier != nil {
		p.notifier.Notify(ctx, meta)
	}

	return nil
}
