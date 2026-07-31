package imessage

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
	syncv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync/v1"
)

const heartbeatEvery = 30 * time.Second

func (c *Crawler) Sync(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*syncv1.TrawlerArchiveSyncReport, error) {
	progress := logProgress(req, "sync_progress", "messages", 0)
	if err := reportProgress(req, progress, "messages", 0, 0, "update started"); err != nil {
		return nil, err
	}
	var result archive.SyncResult
	err := withHeartbeat(ctx, func() error {
		return reportProgress(req, progress, "messages", 0, 0, "update still running")
	}, func() error {
		var syncErr error
		result, syncErr = archive.SyncInto(ctx, req.OpenedTrawlerArchiveStore, archive.SyncOptions{
			ArchivePath:           req.TrawlerArchivePaths.TrawlerArchivePath,
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
	if err := reportProgress(req, progress, "messages", int64(result.Messages), int64(result.Messages), "update complete"); err != nil {
		return nil, err
	}
	return &syncv1.TrawlerArchiveSyncReport{}, nil
}

func logProgress(req *trawlkit.TrawlerCommandExecutionRequest, event, unit string, total int64) *cklog.Progress {
	if req == nil || req.TrawlerCommandLog == nil {
		return nil
	}
	return req.TrawlerCommandLog.Progress(cklog.ProgressOptions{Event: event, Unit: unit, Total: total})
}

func reportProgress(req *trawlkit.TrawlerCommandExecutionRequest, progress *cklog.Progress, phase string, done, total int64, message string) error {
	if req.ReportTrawlerCommandProgress != nil {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: phase, Done: done, Total: total, Message: message})
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

func logSyncTimings(req *trawlkit.TrawlerCommandExecutionRequest, result archive.SyncResult) {
	if req == nil || req.TrawlerCommandLog == nil {
		return
	}
	_ = req.TrawlerCommandLog.Info("sync_done", strings.Join([]string{
		"messages=" + strconv.Itoa(result.Messages),
		"chats=" + strconv.Itoa(result.Chats),
		"participants=" + strconv.Itoa(result.Participants),
		"elapsed_ms=" + elapsedMS(result.TotalElapsed),
	}, " "))
	_ = req.TrawlerCommandLog.Debug("sync_phase", strings.Join([]string{
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
