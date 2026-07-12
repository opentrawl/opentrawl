package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
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

func TestOpenSourceStoreMissingArchiveUsesSharedSafeError(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "synthetic-missing.db")
	_, err := openSourceStore(context.Background(), trawlkit.Paths{Archive: archive}, sourceStoreRead)
	if err == nil {
		t.Fatal("openSourceStore returned nil error")
	}
	var missing trawlkit.MissingArchiveError
	if !errors.As(err, &missing) {
		t.Fatalf("error type = %T, want MissingArchiveError", err)
	}
	body := sourceErrorBody(err)
	if err.Error() != "This source is not ready yet." || body.Code != "unavailable" || body.Message != "This source is not ready yet." || body.Remedy != "" || strings.Contains(err.Error(), archive) || strings.Contains(body.Message, archive) || strings.Contains(body.Remedy, archive) {
		t.Fatalf("missing archive error=%q body=%#v", err, body)
	}
}
