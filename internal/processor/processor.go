package processor

import (
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

// Processor отвечает за обработку событий из ton-indexer.
type Processor struct {
	logger *zap.Logger
}

// NewProcessor создаёт обработчик.
func NewProcessor(logger *zap.Logger) *Processor {
	return &Processor{logger: logger}
}

// Handle обрабатывает единичное событие из ton-indexer.
func (p *Processor) Handle(event ton.Event) error {
	if !event.IsDeploy {
		return nil
	}

	p.logger.Info("обнаружен деплой контракта", zap.String("account", event.AccountAddress), zap.String("code_hash", event.CodeHash))
	return nil
}

