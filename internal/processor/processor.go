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

	// Статистика
	totalProcessed int64
	totalDetected  int64
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

	p.totalProcessed++

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Проверяем дубликаты по адресу (быстрее чем seqno)
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

	// ГЛАВНАЯ ПРОВЕРКА: Верификация по интерфейсу И/ИЛИ code_hash
	// Используем VerifyAndInspect который проверяет get_jetton_data
	meta, err := p.detector.VerifyAndInspect(ctx, event.AccountAddress, codeHash)
	if err != nil {
		if err == detector.ErrNotJettonMinter {
			// Это не Jetton Minter — пропускаем молча
			return nil
		}
		p.logger.Warn("ошибка верификации минтера",
			zap.String("address", event.AccountAddress),
			zap.Error(err),
		)
		return nil
	}

	p.totalDetected++

	// Вычисляем общую задержку обнаружения
	totalLatencyMs := time.Since(event.Timestamp).Milliseconds()
	meta.DetectionLatencyMs = totalLatencyMs

	// Логируем находку с деталями
	p.logger.Info("🚀 НАЙДЕН JETTON MINTER",
		zap.String("address", meta.Address),
		zap.String("name", meta.Name),
		zap.String("symbol", meta.Symbol),
		zap.String("type", meta.MinterType),
		zap.Bool("known_code_hash", meta.KnownCodeHash),
		zap.Bool("verified_by_interface", meta.VerifiedByInterface),
		zap.Int64("latency_ms", totalLatencyMs),
		zap.Int32("workchain", event.Workchain),
		zap.Uint32("seqno", event.Seqno),
	)

	// Запоминаем адрес в кэше
	if p.cache != nil {
		if err := p.cache.RememberMinter(ctx, meta.Address); err != nil {
			p.logger.Warn("не удалось сохранить минтер в кэш", zap.Error(err))
		}
	}

	// Автоматически добавляем новый code_hash если верифицирован по интерфейсу
	if meta.VerifiedByInterface && !meta.KnownCodeHash {
		p.detector.AddCodeHash(meta.CodeHash, "auto_verified_"+time.Now().Format("2006-01-02"))
	}

	// Отправляем уведомления с расширенными данными
	if p.notifier != nil {
		p.notifier.NotifyWithEvent(ctx, meta, &event)
	}

	return nil
}

// GetStats возвращает статистику обработки.
func (p *Processor) GetStats() (processed, detected int64) {
	return p.totalProcessed, p.totalDetected
}
