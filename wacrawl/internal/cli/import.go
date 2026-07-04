package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/wacrawl/internal/store"
	"github.com/openclaw/wacrawl/internal/whatsappdb"
)

func (a *app) importProgress(command string) (func(whatsappdb.ImportProgress), func()) {
	if a.runLog == nil {
		return func(whatsappdb.ImportProgress) {}, func() {}
	}
	progress := a.runLog.Progress(cklog.ProgressOptions{Event: logEventName(command + "_progress"), Unit: "stage", Total: 5})
	var (
		mu   sync.Mutex
		last = whatsappdb.ImportProgress{Total: 5, Message: "starting sync"}
	)
	report := func(event whatsappdb.ImportProgress) {
		if event.Total <= 0 {
			event.Total = 5
		}
		if strings.TrimSpace(event.Message) == "" {
			event.Message = "syncing"
		}
		mu.Lock()
		last = event
		mu.Unlock()
		_ = progress.Report(event.Done, event.Message)
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				event := last
				mu.Unlock()
				if strings.TrimSpace(event.Message) != "" {
					_ = progress.Report(event.Done, event.Message)
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

func (a *app) runImport(ctx context.Context, command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	copyMedia := fs.Bool("copy-media", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, command)
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(fmt.Errorf("%s takes flags only", command))
	}
	progress, stopProgress := a.importProgress(command)
	defer stopProgress()
	return a.withStore(ctx, func(st *store.Store) error {
		stats, err := whatsappdb.ImportWithOptions(ctx, st, whatsappdb.ImportOptions{SourcePath: *source, CopyMedia: *copyMedia, Progress: progress})
		if err != nil {
			return err
		}
		a.logImportTimings(command, stats)
		return a.print(stats)
	})
}

func (a *app) logImportTimings(command string, stats store.ImportStats) {
	if a.runLog == nil {
		return
	}
	_ = a.runLog.Info(logEventName(command+"_done"), strings.Join([]string{
		"messages=" + strconv.Itoa(stats.Messages),
		"chats=" + strconv.Itoa(stats.Chats),
		"participants=" + strconv.Itoa(stats.Participants),
		"media_messages=" + strconv.Itoa(stats.MediaMessages),
		"elapsed_ms=" + elapsedMS(stats.TotalElapsed),
	}, " "))
	_ = a.runLog.Debug(logEventName(command+"_phase"), strings.Join([]string{
		"source=" + logQuote("whatsapp-desktop"),
		"snapshot_ms=" + elapsedMS(stats.SnapshotElapsed),
		"extract_ms=" + elapsedMS(stats.ExtractElapsed),
		"media_ms=" + elapsedMS(stats.MediaElapsed),
		"write_ms=" + elapsedMS(stats.WriteElapsed),
	}, " "))
}
