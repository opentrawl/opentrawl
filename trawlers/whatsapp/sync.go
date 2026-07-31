package whatsapp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/whatsappdb"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	synccontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync"
)

const heartbeatEvery = 30 * time.Second

func (c *Crawler) Sync(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*synccontract.TrawlerArchiveSyncReport, error) {
	st, err := store.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, err
	}
	progress, stopProgress := importProgress(req)
	defer stopProgress()
	stats, err := whatsappdb.ImportWithOptions(ctx, st, whatsappdb.ImportOptions{
		SourcePath: c.cfg.Source,
		CopyMedia:  c.cfg.CopyMedia,
		Progress:   progress,
	})
	if err != nil {
		return nil, err
	}
	logImportTimings(req, stats)
	return &synccontract.TrawlerArchiveSyncReport{}, nil
}

func importProgress(req *trawlkit.TrawlerCommandExecutionRequest) (func(whatsappdb.ImportProgress), func()) {
	var runProgress *cklog.Progress
	if req.TrawlerCommandLog != nil {
		runProgress = req.TrawlerCommandLog.Progress(cklog.ProgressOptions{Event: "sync_progress", Unit: "stage", Total: 5})
	}
	var mu sync.Mutex
	last := whatsappdb.ImportProgress{Total: 5, Message: "starting update"}
	report := func(event whatsappdb.ImportProgress) {
		if event.Total <= 0 {
			event.Total = 5
		}
		if strings.TrimSpace(event.Message) == "" {
			event.Message = "updating"
		}
		mu.Lock()
		last = event
		mu.Unlock()
		if req.ReportTrawlerCommandProgress != nil {
			req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "sync", Done: event.Done, Total: event.Total, Message: event.Message})
		}
		if runProgress != nil {
			_ = runProgress.Report(event.Done, event.Message)
		}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(heartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				event := last
				mu.Unlock()
				if strings.TrimSpace(event.Message) != "" {
					report(event)
				}
			case <-done:
				return
			}
		}
	}()
	stop := func() {
		close(done)
		<-stopped
	}
	return report, stop
}

func logImportTimings(req *trawlkit.TrawlerCommandExecutionRequest, stats store.ImportStats) {
	if req.TrawlerCommandLog == nil {
		return
	}
	_ = req.TrawlerCommandLog.Info("sync_done", strings.Join([]string{
		"messages=" + strconv.Itoa(stats.Messages),
		"chats=" + strconv.Itoa(stats.Chats),
		"participants=" + strconv.Itoa(stats.Participants),
		"media_messages=" + strconv.Itoa(stats.MediaMessages),
		"elapsed_ms=" + elapsedMS(stats.TotalElapsed),
	}, " "))
	_ = req.TrawlerCommandLog.Debug("sync_phase", strings.Join([]string{
		"source=" + logQuote("whatsapp-desktop"),
		"snapshot_ms=" + elapsedMS(stats.SnapshotElapsed),
		"extract_ms=" + elapsedMS(stats.ExtractElapsed),
		"media_ms=" + elapsedMS(stats.MediaElapsed),
		"write_ms=" + elapsedMS(stats.WriteElapsed),
	}, " "))
}

func elapsedMS(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}

func logQuote(value string) string {
	return fmt.Sprintf("%q", value)
}
