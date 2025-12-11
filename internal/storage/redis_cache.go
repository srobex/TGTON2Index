package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	seqnoSetKey        = "hsi:seqno"
	minterKeyPrefix    = "hsi:minter:"
	defaultSeqnoWindow = 1000
)

// RedisCache отвечает за антидубли по seqno и minter.
type RedisCache struct {
	client      *redis.Client
	seqnoWindow int64
	minterTTL   time.Duration
}

// NewRedisCache создаёт клиента Redis.
func NewRedisCache(addr string, ttl time.Duration, seqnoWindow int) (*RedisCache, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis addr пустой")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	win := defaultSeqnoWindow
	if seqnoWindow > 0 {
		win = int64(seqnoWindow)
	}

	return &RedisCache{
		client:      client,
		seqnoWindow: win,
		minterTTL:   ttl,
	}, nil
}

// Close закрывает подключение Redis.
func (c *RedisCache) Close() error {
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

// RegisterSeqno сохраняет seqno и возвращает true, если он новый.
func (c *RedisCache) RegisterSeqno(ctx context.Context, seqno uint32) (bool, error) {
	if seqno == 0 {
		return true, nil
	}

	added, err := c.client.ZAddNX(ctx, seqnoSetKey, redis.Z{Score: float64(seqno), Member: seqno}).Result()
	if err != nil {
		return false, err
	}

	if added == 0 {
		return false, nil
	}

	if err := c.trimSeqno(ctx); err != nil {
		return false, err
	}

	return true, nil
}

// IsMinterKnown проверяет, не обрабатывали ли мы этот адрес ранее.
func (c *RedisCache) IsMinterKnown(ctx context.Context, address string) (bool, error) {
	key := c.minterKey(address)
	exists, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

// RememberMinter помечает адрес как обработанный.
func (c *RedisCache) RememberMinter(ctx context.Context, address string) error {
	key := c.minterKey(address)
	return c.client.Set(ctx, key, 1, c.minterTTL).Err()
}

func (c *RedisCache) trimSeqno(ctx context.Context) error {
	count, err := c.client.ZCard(ctx, seqnoSetKey).Result()
	if err != nil {
		return err
	}
	if count <= c.seqnoWindow {
		return nil
	}
	extra := count - c.seqnoWindow
	_, err = c.client.ZRemRangeByRank(ctx, seqnoSetKey, 0, extra-1).Result()
	return err
}

func (c *RedisCache) minterKey(address string) string {
	return minterKeyPrefix + strings.ToLower(address)
}




