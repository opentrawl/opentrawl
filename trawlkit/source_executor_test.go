package trawlkit

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func TestSourceExecutorRejectsSuccessReturnedAfterReadDeadline(t *testing.T) {
	source := &testCrawler{statusFn: func(ctx context.Context, _ *Request) (*control.Status, error) {
		<-ctx.Done()
		status := control.NewStatus("testcrawl", "Test")
		return &status, nil
	}}
	executor := NewSourceExecutor(SourceExecutorOptions{
		StateRoot: t.TempDir(),
		Timeout:   time.Millisecond,
		Stderr:    io.Discard,
	})
	_, err := executor.Status(context.Background(), source)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("status error = %v, want deadline exceeded", err)
	}
}
