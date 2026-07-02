package cli

import (
	"context"
	"time"

	crawlog "github.com/openclaw/crawlkit/log"
)

const heartbeatEvery = 30 * time.Second

func withHeartbeat(ctx context.Context, progress *crawlog.Progress, done int64, message string, fn func() error) error {
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- fn()
	}()
	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case err := <-doneCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := progress.Report(done, message); err != nil {
				return err
			}
		}
	}
}
