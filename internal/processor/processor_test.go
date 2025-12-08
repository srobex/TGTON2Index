package processor

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/yourname/hyper-sniper-indexer/internal/detector"
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

type testCache struct {
	minters map[string]bool
}

func (c *testCache) RegisterSeqno(context.Context, uint32) (bool, error) { return true, nil }
func (c *testCache) IsMinterKnown(_ context.Context, address string) (bool, error) {
	return c.minters[address], nil
}
func (c *testCache) RememberMinter(_ context.Context, address string) error {
	if c.minters == nil {
		c.minters = make(map[string]bool)
	}
	c.minters[address] = true
	return nil
}

type notifierStub struct {
	count int
}

func (n *notifierStub) Notify(context.Context, *detector.Metadata) {
	n.count++
}

type tonClientStub struct {
	stack [][]byte
}

func (t *tonClientStub) Start(context.Context) error                                  { return nil }
func (t *tonClientStub) Subscribe(context.Context, ton.Handler) error                 { return nil }
func (t *tonClientStub) Catchup(context.Context, time.Time, ton.Handler) error        { return nil }
func (t *tonClientStub) RunGetMethod(context.Context, string, string, ...any) ([][]byte, error) {
	return t.stack, nil
}
func (t *tonClientStub) GetCodeHash(context.Context, string) (string, error) {
	return "6d9f5c5d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b", nil
}

func TestProcessorHandleTriggersNotifier(t *testing.T) {
	logger := zap.NewNop()

	dec := make([]byte, 8)
	binary.BigEndian.PutUint64(dec, 6)

	client := &tonClientStub{
		stack: [][]byte{
			[]byte("Name"),
			[]byte("SYM"),
			dec,
		},
	}

	det := detector.NewDetector(client, logger)
	cache := &testCache{minters: make(map[string]bool)}
	notifier := &notifierStub{}

	proc := NewProcessor(det, client, cache, notifier, logger)

	err := proc.Handle(ton.Event{
		AccountAddress: "0:abcdef",
		CodeHash:       "",
		Timestamp:      time.Now(),
		Seqno:          10,
		IsDeploy:       true,
	})
	if err != nil {
		t.Fatalf("handle returned error: %v", err)
	}

	if !cache.minters["0:abcdef"] {
		t.Fatalf("minter was not cached")
	}

	if notifier.count != 1 {
		t.Fatalf("notifier should be called once, got %d", notifier.count)
	}
}

