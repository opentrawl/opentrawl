package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

func TestWithSourceRequestContextStopsBeforeRuntimeWorkWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime := &Runtime{ctx: ctx, timeout: time.Second}
	called := false
	err := runtime.withSourceRequestContext(ctx, Source{}, "status", sourceStoreOptional, ckoutput.JSON, io.Discard, func(context.Context, *trawlkit.Request) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("err = %v, callback called=%t", err, called)
	}
}

func TestWithSourceRequestKeepsLegacyTimeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	runtime := &Runtime{ctx: ctx, timeout: time.Second}
	err := runtime.withSourceRequest(Source{}, "search", sourceStoreRead, ckoutput.JSON, io.Discard, func(context.Context, *trawlkit.Request) error { return nil })
	if err == nil || !isTimeoutError(err) {
		t.Fatalf("legacy err = %v", err)
	}
}
