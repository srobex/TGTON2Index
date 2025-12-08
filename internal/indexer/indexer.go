package indexer

import (
	"context"
	"fmt"

	"github.com/yourname/hyper-sniper-indexer/internal/config"
	"github.com/yourname/hyper-sniper-indexer/internal/processor"
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

// Service управляет жизненным циклом тон-индексера и обработчиком транзакций.
type Service struct {
	cfg       *config.Config
	client    ton.Client
	processor *processor.Processor
	logger    *zap.Logger
	cancel    context.CancelFunc
}

// NewService создаёт сервис индексатора.
func NewService(cfg *config.Config, client ton.Client, processor *processor.Processor, logger *zap.Logger) *Service {
	return &Service{
		cfg:       cfg,
		client:    client,
		processor: processor,
		logger:    logger,
	}
}

// Start запускает клиент ton-indexer.
func (s *Service) Start(ctx context.Context) error {
	if s.client == nil {
		return fmt.Errorf("ton client не инициализирован")
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	if err := s.client.Start(runCtx); err != nil {
		return fmt.Errorf("не удалось запустить ton-indexer: %w", err)
	}

	s.logger.Info("ton-indexer запущен", zap.String("network", s.cfg.App.Network))

	go s.runRealtime(runCtx)
	return nil
}

// Stop останавливает сервис.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Service) runRealtime(ctx context.Context) {
	handler := func(event ton.Event) error {
		if s.processor == nil {
			return fmt.Errorf("processor не инициализирован")
		}
		return s.processor.Handle(event)
	}

	if err := s.client.Subscribe(ctx, handler); err != nil {
		s.logger.Error("подписка realtime завершилась с ошибкой", zap.Error(err))
	}
}

