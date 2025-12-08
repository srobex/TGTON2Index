package ton

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Event описывает минимальный набор данных о транзакции с деплоем.
type Event struct {
	AccountAddress string
	CodeHash       string
	Timestamp      time.Time
	Seqno          uint32
	Workchain      int32
	Shard          int64
	IsDeploy       bool
}

// Handler получает события из индексатора.
type Handler func(event Event) error

// Client определяет контракт для работы с ton-indexer.
type Client interface {
	Start(ctx context.Context) error
	Subscribe(ctx context.Context, handler Handler) error
	Catchup(ctx context.Context, since time.Time, handler Handler) error
	RunGetMethod(ctx context.Context, address string, method string, stack ...any) ([][]byte, error)
	GetCodeHash(ctx context.Context, address string) (string, error)
}

// IndexerClient — заглушечная реализация, оборачивающая ton-org/ton-indexer.
// Методы сейчас возвращают ошибки, пока не будет подключён настоящий индексатор.
type IndexerClient struct {
	network     string
	liteservers []string
	logger      *zap.Logger
}

// NewIndexerClient создаёт клиента для выбранной сети.
func NewIndexerClient(network string, liteservers []string, logger *zap.Logger) *IndexerClient {
	return &IndexerClient{
		network:     network,
		liteservers: liteservers,
		logger:      logger,
	}
}

// Start подготавливает соединения с liteserver'ами.
func (c *IndexerClient) Start(_ context.Context) error {
	if len(c.liteservers) == 0 {
		c.logger.Info("liteservers не указаны, будет использован список из global-config.json")
	}
	return nil
}

// Subscribe подключается к потоку новых транзакций.
func (c *IndexerClient) Subscribe(_ context.Context, _ Handler) error {
	return errors.New("Subscribe не реализован: требуется подключить ton-indexer")
}

// Catchup выгружает исторические данные с указанного момента.
func (c *IndexerClient) Catchup(_ context.Context, _ time.Time, _ Handler) error {
	return errors.New("Catchup не реализован: требуется подключить ton-indexer")
}

// RunGetMethod вызывает get-метод контракта.
func (c *IndexerClient) RunGetMethod(_ context.Context, _ string, _ string, _ ...any) ([][]byte, error) {
	return nil, errors.New("RunGetMethod не реализован: требуется подключить ton-indexer")
}

// GetCodeHash возвращает code_hash аккаунта.
func (c *IndexerClient) GetCodeHash(_ context.Context, address string) (string, error) {
	if address == "" {
		return "", fmt.Errorf("пустой адрес")
	}
	return "", errors.New("GetCodeHash не реализован: требуется подключить ton-indexer")
}

