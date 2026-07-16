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
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

type sourceRuntimePreparer struct {
	archive  string
	prepared bool
}

type sourceRuntimeWritePreparer struct {
	archive      string
	prepareCalls int
}

func (c *sourceRuntimeWritePreparer) Info() trawlkit.Info {
	return trawlkit.Info{ID: "write-only", Surface: "write-only", DisplayName: "Write only", DefaultPaths: trawlkit.Paths{Archive: c.archive}}
}

func (c *sourceRuntimeWritePreparer) Status(context.Context, *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus("write-only", "write-only")
	return &status, nil
}

func (c *sourceRuntimeWritePreparer) Verbs() []trawlkit.Verb { return nil }

func (c *sourceRuntimeWritePreparer) PrepareArchive(context.Context, string) error {
	c.prepareCalls++
	return nil
}

func (c *sourceRuntimePreparer) Info() trawlkit.Info {
	return trawlkit.Info{ID: "prepared", Surface: "prepared", DisplayName: "Prepared", DefaultPaths: trawlkit.Paths{Archive: c.archive}}
}

func (c *sourceRuntimePreparer) Status(context.Context, *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus("prepared", "prepared")
	return &status, nil
}

func (c *sourceRuntimePreparer) Verbs() []trawlkit.Verb { return nil }

func (c *sourceRuntimePreparer) PrepareReadArchive(ctx context.Context, path string) error {
	c.prepared = path == c.archive
	st, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		return err
	}
	return st.Close()
}

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

func TestWithSourceRequestPreparesBeforeOptionalAndReadStores(t *testing.T) {
	for _, access := range []sourceStoreAccess{sourceStoreOptional, sourceStoreRead} {
		t.Run(map[sourceStoreAccess]string{sourceStoreOptional: "optional", sourceStoreRead: "read"}[access], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			crawler := &sourceRuntimePreparer{archive: filepath.Join(t.TempDir(), "prepared.db")}
			runtime := &Runtime{ctx: context.Background(), timeout: time.Second, stderr: io.Discard}
			err := runtime.withSourceRequest(Source{ID: "prepared", Crawler: crawler}, "status", access, ckoutput.JSON, io.Discard, func(_ context.Context, req *trawlkit.Request) error {
				if req.Store == nil {
					t.Fatal("runner opened no store after preparation")
				}
				return nil
			})
			if err != nil || !crawler.prepared {
				t.Fatalf("preparation err=%v prepared=%t", err, crawler.prepared)
			}
		})
	}
}

func TestWithSourceRequestDoesNotRunWriteArchivePreparationForReadStores(t *testing.T) {
	for _, access := range []sourceStoreAccess{sourceStoreOptional, sourceStoreRead} {
		t.Run(map[sourceStoreAccess]string{sourceStoreOptional: "optional", sourceStoreRead: "read"}[access], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			crawler := &sourceRuntimeWritePreparer{archive: filepath.Join(t.TempDir(), "missing.db")}
			runtime := &Runtime{ctx: context.Background(), timeout: time.Second, stderr: io.Discard}
			err := runtime.withSourceRequest(Source{ID: "write-only", Crawler: crawler}, "status", access, ckoutput.JSON, io.Discard, func(context.Context, *trawlkit.Request) error {
				return nil
			})
			if access == sourceStoreOptional && err != nil {
				t.Fatalf("optional store err = %v", err)
			}
			if access == sourceStoreRead && err == nil {
				t.Fatal("read store err = nil, want missing archive error")
			}
			if crawler.prepareCalls != 0 {
				t.Fatalf("PrepareArchive calls = %d, want 0 for a read store", crawler.prepareCalls)
			}
		})
	}
}
