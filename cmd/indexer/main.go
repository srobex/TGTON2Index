package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yourname/hyper-sniper-indexer/internal/config"
	"github.com/yourname/hyper-sniper-indexer/internal/indexer"
	"github.com/yourname/hyper-sniper-indexer/internal/processor"
	"github.com/yourname/hyper-sniper-indexer/internal/utils"
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

// Точка входа индексатора: загрузка конфига и инициализация сервисов.
func main() {
	logger, err := utils.NewLogger()
	if err != nil {
		log.Fatalf("не удалось создать логгер: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	cfg, err := config.Load(configPath())
	if err != nil {
		logger.Fatal("ошибка загрузки конфига", zap.Error(err))
	}

	tonClient := ton.NewIndexerClient(cfg.App.Network, cfg.App.Liteservers, logger)
	proc := processor.NewProcessor(logger)
	svc := indexer.NewService(cfg, tonClient, proc, logger)

	ctx, cancel := signalContext()
	defer cancel()

	if err := svc.Start(ctx); err != nil {
		logger.Fatal("ошибка запуска индексатора", zap.Error(err))
	}

	<-ctx.Done()
	svc.Stop()
}

func configPath() string {
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}
	return "config.yaml"
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		defer signal.Stop(signals)
		<-signals
		cancel()
	}()

	return ctx, cancel
}

