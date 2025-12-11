package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yourname/hyper-sniper-indexer/internal/config"
	"github.com/yourname/hyper-sniper-indexer/internal/detector"
	"github.com/yourname/hyper-sniper-indexer/internal/indexer"
	"github.com/yourname/hyper-sniper-indexer/internal/notifier"
	"github.com/yourname/hyper-sniper-indexer/internal/processor"
	"github.com/yourname/hyper-sniper-indexer/internal/storage"
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

	networkFlag := flag.String("network", "", "mainnet или testnet")
	flag.Parse()

	cfg, err := config.Load(configPath())
	if err != nil {
		logger.Fatal("ошибка загрузки конфига", zap.Error(err))
	}

	if *networkFlag != "" {
		cfg.App.Network = *networkFlag
	}
	if cfg.App.Network != "mainnet" && cfg.App.Network != "testnet" {
		logger.Fatal("некорректная сеть", zap.String("network", cfg.App.Network))
	}

	logger.Info("🚀 Запуск HyperSniper Indexer",
		zap.String("network", cfg.App.Network),
		zap.Int("catchup_hours", cfg.App.CatchupHours),
	)

	// Инициализируем хранилище (Redis)
	store, err := storage.NewStorage(cfg)
	if err != nil {
		logger.Fatal("ошибка инициализации хранилища", zap.Error(err))
	}
	defer store.Close()
	logger.Info("✅ Redis подключён", zap.String("addr", cfg.Redis.Addr))

	// Создаём TON клиент
	tonClient := ton.NewIndexerClient(cfg.App.Network, cfg.App.Liteservers, logger)

	// Подключаемся к TON
	ctx, cancel := signalContext()
	defer cancel()

	if err := tonClient.Start(ctx); err != nil {
		logger.Fatal("ошибка подключения к TON", zap.Error(err))
	}
	logger.Info("✅ Подключение к TON установлено")

	// Создаём детектор (передаём TON клиент как MetadataFetcher)
	det := detector.NewDetector(tonClient, logger)
	det.LoadRealCodeHashes() // Загружаем реальные хэши
	logger.Info("✅ Детектор инициализирован", zap.Int("known_hashes", len(det.GetKnownHashes())))

	// Создаём нотификатор
	ntf := notifier.New(cfg, logger)
	if cfg.Notifier.TgBotToken != "" {
		logger.Info("✅ Telegram уведомления включены", zap.String("chat_id", cfg.Notifier.TgChatID))
	} else {
		logger.Info("⚠️ Telegram уведомления отключены (токен не указан)")
	}

	// Создаём процессор
	proc := processor.NewProcessor(det, tonClient, store.Cache, ntf, logger)

	// Создаём и запускаем сервис индексатора
	svc := indexer.NewService(cfg, tonClient, proc, logger)

	if err := svc.Start(ctx); err != nil {
		logger.Fatal("ошибка запуска индексатора", zap.Error(err))
	}

	logger.Info("✅ Индексатор запущен, сканируем блокчейн TON...")
	logger.Info("📊 Цель: обнаружение новых Jetton Minter за 1-2 секунды")

	// Ждём сигнала завершения
	<-ctx.Done()
	logger.Info("🛑 Получен сигнал завершения, останавливаемся...")
	svc.Stop()
	logger.Info("✅ Индексатор остановлен")
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
