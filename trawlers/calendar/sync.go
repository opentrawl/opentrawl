package calcrawl

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/calendarstore"
)

const heartbeatEvery = 30 * time.Second

func (c *Crawler) Sync(ctx context.Context, req *crawlkit.Request) (*crawlkit.SyncReport, error) {
	syncStarted := time.Now()
	sourceProgress := req.Log.Progress(cklog.ProgressOptions{Event: "source_progress", Unit: "events"})
	if err := reportProgress(req, sourceProgress, "source", 0, 0, "reading Calendar source"); err != nil {
		return nil, err
	}
	var data calendarstore.Data
	sourceStarted := time.Now()
	err := withHeartbeat(ctx, func() error {
		return reportProgress(req, sourceProgress, "source", 0, 0, "reading Calendar source")
	}, func() error {
		var readErr error
		data, readErr = calendarstore.Read(ctx, calendarstore.DefaultPath())
		return readErr
	})
	sourceElapsed := time.Since(sourceStarted)
	if err != nil {
		return nil, sourceErr(fmt.Errorf("read Calendar source: %w", err))
	}
	if err := reportProgress(req, sourceProgress, "source", int64(len(data.Events)), 0, "read Calendar source"); err != nil {
		return nil, err
	}
	st, err := archive.Use(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, err
	}
	archiveProgress := req.Log.Progress(cklog.ProgressOptions{Event: "archive_progress", Unit: "events", Total: int64(len(data.Events))})
	if err := reportProgress(req, archiveProgress, "archive", 0, int64(len(data.Events)), "writing archive"); err != nil {
		return nil, err
	}
	var stats archive.SyncStats
	archiveStarted := time.Now()
	err = withHeartbeat(ctx, func() error {
		return reportProgress(req, archiveProgress, "archive", int64(len(data.Events)), int64(len(data.Events)), "writing archive")
	}, func() error {
		var applyErr error
		stats, applyErr = st.ApplySnapshot(ctx, archiveCalendars(data.Calendars), archiveEvents(data.Events), archive.NewRunID(), time.Now(), data.SourcePath, data.SourceModifiedAt)
		return applyErr
	})
	archiveElapsed := time.Since(archiveStarted)
	if err != nil {
		return nil, err
	}
	if err := reportProgress(req, archiveProgress, "archive", int64(len(data.Events)), int64(len(data.Events)), "wrote archive"); err != nil {
		return nil, err
	}
	logSyncTimings(req, stats, time.Since(syncStarted), sourceElapsed, archiveElapsed)
	return &crawlkit.SyncReport{
		Added:   int64(stats.NewEvents),
		Updated: int64(stats.ChangedEvents),
		Removed: int64(stats.DeletedEvents),
	}, nil
}

func reportProgress(req *crawlkit.Request, progress *cklog.Progress, phase string, done, total int64, message string) error {
	if req.Progress != nil {
		req.Progress(crawlkit.Progress{Phase: phase, Done: done, Total: total, Message: message})
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

func logSyncTimings(req *crawlkit.Request, stats archive.SyncStats, totalElapsed, sourceElapsed, archiveElapsed time.Duration) {
	_ = req.Log.Info("sync_done", strings.Join([]string{
		"calendars=" + strconv.Itoa(stats.Calendars),
		"events=" + strconv.Itoa(stats.Events),
		"new=" + strconv.Itoa(stats.NewEvents),
		"changed=" + strconv.Itoa(stats.ChangedEvents),
		"deleted=" + strconv.Itoa(stats.DeletedEvents),
		"elapsed_ms=" + elapsedMS(totalElapsed),
	}, " "))
	_ = req.Log.Debug("sync_phase", strings.Join([]string{
		"source=" + logQuote("calendar_store"),
		"read_ms=" + elapsedMS(sourceElapsed),
		"write_ms=" + elapsedMS(archiveElapsed),
	}, " "))
}

func archiveCalendars(calendars []calendarstore.Calendar) []archive.Calendar {
	out := make([]archive.Calendar, 0, len(calendars))
	for _, calendar := range calendars {
		out = append(out, archive.Calendar{
			ID:              strconv.FormatInt(calendar.RowID, 10),
			SourceRowID:     calendar.RowID,
			Title:           strings.TrimSpace(calendar.Title),
			Type:            calendar.Type,
			ExternalID:      strings.TrimSpace(calendar.ExternalID),
			StoreID:         calendar.StoreID,
			AccountName:     strings.TrimSpace(calendar.StoreName),
			AccountType:     calendar.StoreType,
			AccountDisabled: calendar.StoreDisabled,
		})
	}
	return out
}

func archiveEvents(events []calendarstore.Event) []archive.Event {
	out := make([]archive.Event, 0, len(events))
	for _, event := range events {
		uid := eventUID(event)
		calendarID := strconv.FormatInt(event.Calendar.RowID, 10)
		attendees := archiveAttendees(event.Attendees)
		location := archive.Location{Title: strings.TrimSpace(event.Location.Title), Address: strings.TrimSpace(event.Location.Address)}
		out = append(out, archive.Event{
			UID:              uid,
			SourceRowID:      event.RowID,
			UUID:             strings.TrimSpace(event.UUID),
			UniqueIdentifier: strings.TrimSpace(event.UniqueIdentifier),
			Calendar: archive.CalendarProvenance{
				ID:         calendarID,
				Title:      strings.TrimSpace(event.Calendar.Title),
				Type:       event.Calendar.Type,
				ExternalID: strings.TrimSpace(event.Calendar.ExternalID),
			},
			Account: archive.AccountProvenance{
				Name: strings.TrimSpace(event.Calendar.StoreName),
				Type: event.Calendar.StoreType,
			},
			Start:              event.Start.Value,
			End:                event.End.Value,
			StartUnix:          event.Start.Unix,
			EndUnix:            event.End.Unix,
			AllDay:             event.AllDay,
			Summary:            strings.TrimSpace(event.Summary),
			Description:        strings.TrimSpace(event.Description),
			Status:             archive.NormalizeEventStatus(event.Status),
			URL:                strings.TrimSpace(event.URL),
			HasRecurrences:     event.HasRecurrences,
			Availability:       event.Availability,
			Organizer:          archivePerson(event.Organizer),
			Location:           location,
			Attendees:          attendees,
			ParticipantsText:   participantsText(attendees),
			LocationSearchText: locationSearchText(location),
		})
	}
	return out
}

func eventUID(event calendarstore.Event) string {
	for _, value := range []string{event.UUID, event.UniqueIdentifier} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "row:" + strconv.FormatInt(event.RowID, 10)
}

func archivePerson(person calendarstore.Person) archive.Person {
	return archive.Person{
		DisplayName: strings.TrimSpace(person.DisplayName),
		Email:       strings.TrimSpace(person.Email),
		PhoneNumber: strings.TrimSpace(person.PhoneNumber),
	}
}

func archiveAttendees(items []calendarstore.Participant) []archive.Attendee {
	out := make([]archive.Attendee, 0, len(items))
	for _, item := range items {
		out = append(out, archive.Attendee{
			DisplayName: strings.TrimSpace(item.DisplayName),
			Email:       strings.TrimSpace(item.Email),
			PhoneNumber: strings.TrimSpace(item.PhoneNumber),
			Address:     strings.TrimSpace(item.Address),
			RSVPStatus:  strings.TrimSpace(item.RSVPStatus),
			Role:        strings.TrimSpace(item.Role),
			Self:        item.Self,
			Comment:     strings.TrimSpace(item.Comment),
		})
	}
	return out
}

func participantsText(attendees []archive.Attendee) string {
	parts := make([]string, 0, len(attendees)*4)
	for _, attendee := range attendees {
		parts = appendIfNotEmpty(parts, attendee.DisplayName)
		parts = appendIfNotEmpty(parts, attendee.Email)
		parts = appendIfNotEmpty(parts, attendee.PhoneNumber)
		parts = appendIfNotEmpty(parts, attendee.RSVPStatus)
	}
	return strings.Join(parts, " ")
}

func locationSearchText(location archive.Location) string {
	return strings.TrimSpace(strings.Join([]string{location.Title, location.Address}, " "))
}

func appendIfNotEmpty(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	return append(values, strings.TrimSpace(value))
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
