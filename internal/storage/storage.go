package storage

import (
	"fmt"
	"time"

	"github.com/yourname/hyper-sniper-indexer/internal/config"
)

// Storage агрегирует клиенты Redis/PostgreSQL.
type Storage struct {
	Cache *RedisCache
}

// NewStorage создаёт все подключения.
func NewStorage(cfg *config.Config) (*Storage, error) {
	cache, err := NewRedisCache(cfg.Redis.Addr, cfg.MinterCacheDuration(), cfg.App.MasterSeqnoCacheSize)
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}

	return &Storage{Cache: cache}, nil
}

// Close корректно закрывает клиенты.
func (s *Storage) Close() error {
	if s.Cache != nil {
		return s.Cache.Close()
	}
	return nil
}

// Dummy PG placeholder to comply with будущей инициализацией.
func (s *Storage) WaitForPostgres(_ time.Duration) error {
	return nil
}

