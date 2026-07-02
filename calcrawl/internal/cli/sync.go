package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/calendarstore"
)

type progressEvent struct {
	Event string `json:"event"`
	Stage string `json:"stage"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type syncCompleteEvent struct {
	Event            string `json:"event"`
	State            string `json:"state"`
	Calendars        int    `json:"calendars"`
	Events           int    `json:"events"`
	NewEvents        int    `json:"new_events"`
	ChangedEvents    int    `json:"changed_events"`
	UnchangedEvents  int    `json:"unchanged_events"`
	DeletedEvents    int    `json:"deleted_events"`
	SyncedAt         string `json:"synced_at"`
	Source           string `json:"source_path"`
	SourceModifiedAt string `json:"source_modified_at"`
	Archive          string `json:"archive_path"`
}

func (r *runtime) runSync(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"sync"})
	}
	fs, err := r.parseNoFlags("sync", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("sync takes no arguments"))
	}
	data, err := calendarstore.Read(r.ctx, calendarstore.DefaultPath())
	if err != nil {
		return sourceErr(fmt.Errorf("read Calendar source: %w", err))
	}
	if err := r.progress("source", len(data.Events), len(data.Events)); err != nil {
		return err
	}
	st, err := archive.Open(r.ctx, archive.DefaultPath())
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	stats, err := st.ApplySnapshot(r.ctx, archiveCalendars(data.Calendars), archiveEvents(data.Events), archive.NewRunID(), time.Now(), data.SourcePath, data.SourceModifiedAt)
	if err != nil {
		return err
	}
	return r.syncComplete(syncCompleteEvent{
		Event:            "complete",
		State:            "ok",
		Calendars:        stats.Calendars,
		Events:           stats.Events,
		NewEvents:        stats.NewEvents,
		ChangedEvents:    stats.ChangedEvents,
		UnchangedEvents:  stats.UnchangedEvents,
		DeletedEvents:    stats.DeletedEvents,
		SyncedAt:         stats.SyncedAt,
		Source:           stats.SourcePath,
		SourceModifiedAt: stats.SourceModifiedAt,
		Archive:          stats.ArchivePath,
	})
}

func (r *runtime) progress(stage string, done, total int) error {
	event := progressEvent{Event: "progress", Stage: stage, Done: done, Total: total}
	if r.json {
		return r.printJSONLine(event)
	}
	_, err := fmt.Fprintf(r.stderr, "%s: %d/%d\n", stage, done, total)
	return err
}

func (r *runtime) syncComplete(event syncCompleteEvent) error {
	if r.json {
		return r.printJSONLine(event)
	}
	_, err := fmt.Fprintf(r.stdout, "Sync complete: %d calendars, %d events archived; %d new, %d changed.\n",
		event.Calendars, event.Events, event.NewEvents, event.ChangedEvents)
	return err
}

func archiveCalendars(calendars []calendarstore.Calendar) []archive.Calendar {
	out := make([]archive.Calendar, 0, len(calendars))
	for _, calendar := range calendars {
		out = append(out, archive.Calendar{
			ID:              strconv.FormatInt(calendar.RowID, 10),
			SourceRowID:     calendar.RowID,
			Title:           fallbackTitle(calendar.Title, "Calendar"),
			Type:            calendar.Type,
			ExternalID:      strings.TrimSpace(calendar.ExternalID),
			StoreID:         calendar.StoreID,
			AccountName:     fallbackTitle(calendar.StoreName, "Default"),
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
				Title:      fallbackTitle(event.Calendar.Title, "Calendar"),
				Type:       event.Calendar.Type,
				ExternalID: strings.TrimSpace(event.Calendar.ExternalID),
			},
			Account: archive.AccountProvenance{
				Name: fallbackTitle(event.Calendar.StoreName, "Default"),
				Type: event.Calendar.StoreType,
			},
			Start:              event.Start.Value,
			End:                event.End.Value,
			StartUnix:          event.Start.Unix,
			EndUnix:            event.End.Unix,
			AllDay:             event.AllDay,
			Summary:            strings.TrimSpace(event.Summary),
			Description:        strings.TrimSpace(event.Description),
			Status:             strings.TrimSpace(event.Status),
			URL:                strings.TrimSpace(event.URL),
			HasRecurrences:     event.HasRecurrences,
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

func fallbackTitle(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func appendIfNotEmpty(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	return append(values, strings.TrimSpace(value))
}
