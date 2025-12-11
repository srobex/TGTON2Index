package detector

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
	"go.uber.org/zap"
)

type fakeTonClient struct {
	stack [][]byte
}

func (f *fakeTonClient) Start(context.Context) error                                  { return nil }
func (f *fakeTonClient) Subscribe(context.Context, ton.Handler) error                 { return nil }
func (f *fakeTonClient) Catchup(context.Context, time.Time, ton.Handler) error        { return nil }
func (f *fakeTonClient) RunGetMethod(context.Context, string, string, ...any) ([][]byte, error) {
	return f.stack, nil
}
func (f *fakeTonClient) GetCodeHash(context.Context, string) (string, error) { return "", nil }

func TestIsJettonMinter(t *testing.T) {
	logger := zap.NewNop()
	d := NewDetector(&fakeTonClient{}, logger)

	if !d.IsJettonMinter("6d9f5c5d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b") {
		t.Fatalf("expected hash to be recognized")
	}

	if d.IsJettonMinter("deadbeef") {
		t.Fatalf("unexpected hash accepted")
	}
}

func TestInspectParsesMetadata(t *testing.T) {
	logger := zap.NewNop()

	dec := make([]byte, 8)
	binary.BigEndian.PutUint64(dec, 9)

	fake := &fakeTonClient{
		stack: [][]byte{
			[]byte("TestName"),
			[]byte("TST"),
			dec,
		},
	}

	d := NewDetector(fake, logger)

	meta, err := d.Inspect(context.Background(), "addr", "6d9f5c5d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b")
	if err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	if meta.Name != "TestName" || meta.Symbol != "TST" || meta.Decimals != 9 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}




