package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"strconv"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) runStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "status")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes flags only"))
	}
	logTail := a.logTail()
	err := a.withReadStore(ctx, func(st *store.Store) error {
		status, err := st.Status(ctx)
		if err != nil {
			return err
		}
		if a.json {
			return a.print(newStatusEnvelope(status, logTail))
		}
		return a.printStatus(status, logTail)
	})
	if errors.Is(err, errNoArchive) {
		if a.json {
			return a.print(statusEnvelope{
				AppID:   "wacrawl",
				State:   "missing",
				Summary: errNoArchive.Error(),
				Counts:  []statusCount{},
				Log:     statusLogOutput(logTail),
			})
		}
		return render.WriteStatus(a.stdout, render.Status{
			State:   render.StatusMissing,
			Summary: errNoArchive.Error(),
			Log:     renderLogTail(logTail),
		})
	}
	return err
}

type statusEnvelope struct {
	AppID     string                `json:"app_id"`
	State     string                `json:"state"`
	Summary   string                `json:"summary,omitempty"`
	Freshness *freshnessEnvelope    `json:"freshness,omitempty"`
	Counts    []statusCount         `json:"counts"`
	Log       *render.DoctorLogTail `json:"log,omitempty"`
}

type freshnessEnvelope struct {
	LastSync string `json:"last_sync,omitempty"`
}

type statusCount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

func newStatusEnvelope(status store.Status, logTail logTailEnvelope) statusEnvelope {
	state := "ok"
	summary := "archive ready"
	if status.Messages == 0 {
		state = "empty"
		if status.LastImportAt.IsZero() {
			summary = "archive is empty; run wacrawl sync to populate it"
		} else {
			summary = "archive contains no messages from the last sync"
		}
	}
	var freshness *freshnessEnvelope
	if !status.LastImportAt.IsZero() {
		freshness = &freshnessEnvelope{LastSync: formatTime(status.LastImportAt)}
	}
	var since any
	if !status.OldestMessage.IsZero() {
		since = status.OldestMessage.In(time.Local).Year()
	}
	return statusEnvelope{
		AppID:     "wacrawl",
		State:     state,
		Summary:   summary,
		Freshness: freshness,
		Log:       statusLogOutput(logTail),
		Counts: []statusCount{
			{ID: "messages", Label: "messages", Value: status.Messages},
			{ID: "chats", Label: "chats", Value: status.Chats},
			{ID: "since", Label: "since", Value: since},
		},
	}
}

func statusLogOutput(logTail logTailEnvelope) *render.DoctorLogTail {
	return render.DoctorLogTailOutput(render.LogTail{
		LastRun:         statusLogRunOutput(logTail.LastRun),
		MostRecentError: statusLogErrorOutput(logTail.Error),
	})
}

func statusLogRunOutput(run *logRunEnvelope) *cklog.RunSummary {
	if run == nil {
		return nil
	}
	return &cklog.RunSummary{
		Command:    run.Command,
		Outcome:    run.Outcome,
		StartedAt:  parseFormattedTime(run.StartedAt),
		FinishedAt: parseFormattedTime(run.FinishedAt),
	}
}

func statusLogErrorOutput(logError *logErrorEnvelope) *cklog.Line {
	line := renderLogError(logError)
	if line == nil {
		return nil
	}
	line.Level = cklog.LevelError
	line.Visibility = cklog.VisibilityUserFacing
	return line
}

func (a *app) printStatus(status store.Status, logTail logTailEnvelope) error {
	return render.WriteStatus(a.stdout, renderStatus(status, logTail))
}

func renderStatus(status store.Status, logTail logTailEnvelope) render.Status {
	envelope := newStatusEnvelope(status, logTail)
	sections := []render.Section{
		{Title: "Archive", Fields: []render.Field{
			{Label: "Database", Value: status.DBPath},
			{Label: "Source", Value: status.LastSource},
		}},
		{Title: "Messages", Fields: []render.Field{
			{Label: "Messages", Value: strconv.Itoa(status.Messages)},
			{Label: "Media messages", Value: strconv.Itoa(status.MediaMessages)},
			{Label: "Chats", Value: strconv.Itoa(status.Chats)},
			{Label: "Unread chats", Value: strconv.Itoa(status.UnreadChats)},
			{Label: "Unread messages", Value: strconv.Itoa(status.UnreadMessages)},
			{Label: "Contacts", Value: strconv.Itoa(status.Contacts)},
			{Label: "Groups", Value: strconv.Itoa(status.Groups)},
			{Label: "Participants", Value: strconv.Itoa(status.Participants)},
		}},
	}
	if !status.OldestMessage.IsZero() || !status.NewestMessage.IsZero() {
		sections = append(sections, render.Section{Title: "Range", Fields: []render.Field{
			{Label: "Oldest message", Value: formatTime(status.OldestMessage)},
			{Label: "Newest message", Value: formatTime(status.NewestMessage)},
		}})
	}
	var freshness *render.Freshness
	if envelope.Freshness != nil {
		freshness = &render.Freshness{LastSync: envelope.Freshness.LastSync}
	}
	return render.Status{
		State:     renderStatusState(envelope.State),
		Summary:   envelope.Summary,
		Sections:  sections,
		Freshness: freshness,
		Log:       renderLogTail(logTail),
	}
}

func renderStatusState(state string) render.StatusState {
	switch state {
	case "ok":
		return render.StatusOK
	case "empty":
		return render.StatusEmpty
	case "missing":
		return render.StatusMissing
	case "error":
		return render.StatusError
	default:
		return render.StatusUnknown
	}
}
