package processor

import "go.uber.org/zap"

// Processor отвечает за обработку событий из ton-indexer.
type Processor struct {
	logger *zap.Logger
}

// NewProcessor создаёт обработчик.
func NewProcessor(logger *zap.Logger) *Processor {
	return &Processor{logger: logger}
}

