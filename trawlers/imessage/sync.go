package imsgcrawl

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
)

const heartbeatEvery = 30 * time.Second

func (c *Crawler) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	progress := logProgress(req, "sync_progress", "messages", 0)
	if err := reportProgress(req, progress, "messages", 0, 0, "sync started"); err != nil {
		return nil, err
	}
	var result archive.SyncResult
	err := withHeartbeat(ctx, func() error {
		return reportProgress(req, progress, "messages", 0, 0, "sync still running")
	}, func() error {
		var syncErr error
		result, syncErr = archive.SyncInto(ctx, req.Store, archive.SyncOptions{
			ArchivePath:           req.Paths.Archive,
			SourcePath:            messages.DefaultChatDBPath(),
			UseDefaultAddressBook: true,
		})
		return syncErr
	})
	if err != nil {
		if errors.Is(err, archive.ErrArchiveSync) {
			return nil, archiveErr(err)
		}
		return nil, sourceErr(err)
	}
	logSyncTimings(req, result)
	if err := reportProgress(req, progress, "messages", int64(result.Messages), int64(result.Messages), "sync complete"); err != nil {
		return nil, err
	}
	return &trawlkit.SyncReport{Added: int64(result.Messages)}, nil
}

func logProgress(req *trawlkit.Request, event, unit string, total int64) *cklog.Progress {
	if req == nil || req.Log == nil {
		return nil
	}
	return req.Log.Progress(cklog.ProgressOptions{Event: event, Unit: unit, Total: total})
}

func reportProgress(req *trawlkit.Request, progress *cklog.Progress, phase string, done, total int64, message string) error {
	if req.Progress != nil {
		req.Progress(trawlkit.Progress{Phase: phase, Done: done, Total: total, Message: message})
	}
	return progress.Report(done, message)
}

func withHeartbeat(ctx context.Context, progress func() error, fn func() error) error {
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
			if err := progress(); err != nil {
				return err
			}
		}
	}
}

func logSyncTimings(req *trawlkit.Request, result archive.SyncResult) {
	if req == nil || req.Log == nil {
		return
	}
	_ = req.Log.Info("sync_done", strings.Join([]string{
		"messages=" + strconv.Itoa(result.Messages),
		"chats=" + strconv.Itoa(result.Chats),
		"participants=" + strconv.Itoa(result.Participants),
		"elapsed_ms=" + elapsedMS(result.TotalElapsed),
	}, " "))
	_ = req.Log.Debug("sync_phase", strings.Join([]string{
		"source=" + logQuote("messages"),
		"extract_ms=" + elapsedMS(result.ExtractElapsed),
		"contacts_ms=" + elapsedMS(result.ContactsElapsed),
		"map_ms=" + elapsedMS(result.MapElapsed),
		"write_ms=" + elapsedMS(result.WriteElapsed),
	}, " "))
}

func logQuote(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func elapsedMS(value time.Duration) string {
	return strconv.FormatInt(value.Milliseconds(), 10)
}
