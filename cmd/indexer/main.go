package main

import (
	"log"
	"os"

	"github.com/yourname/hyper-sniper-indexer/internal/config"
	"github.com/yourname/hyper-sniper-indexer/internal/utils"
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

	logger.Info("конфиг загружен", zap.String("network", cfg.App.Network))
}

func configPath() string {
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}
	return "config.yaml"
}

