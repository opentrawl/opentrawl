package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func (r *runtime) print(value any) error {
	if r.json {
		enc := json.NewEncoder(r.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch typed := value.(type) {
	case metadataEnvelope:
		return printMetadataText(r.stdout, typed)
	case statusEnvelope:
		return printStatusText(r.stdout, typed)
	case archive.SearchResult:
		return printSearchText(r.stdout, typed)
	case archive.OpenResult:
		return printOpenText(r.stdout, typed)
	case doctorOutput:
		return printDoctorText(r.stdout, typed)
	case control.ContactExport:
		return printContactsText(r.stdout, typed)
	default:
		return json.NewEncoder(r.stdout).Encode(value)
	}
}

func printMetadataText(w io.Writer, value metadataEnvelope) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n%s\n", value.DisplayName, value.ID, value.Description); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\nCapabilities: %s\n", strings.Join(value.Capabilities, ", ")); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nMachine output: add --json for the control manifest.\n")
	return err
}

func printStatusText(w io.Writer, value statusEnvelope) error {
	if _, err := fmt.Fprintf(w, "Status: %s\n%s\n", value.State, value.Summary); err != nil {
		return err
	}
	if value.Archive != nil {
		if _, err := fmt.Fprintf(w, "\nLocal archive:\n  Database: %s\n  Last sync: %s\n  Messages: %d\n  Senders: %d\n  Since: %d\n",
			value.Archive.ArchivePath, emptyDash(value.Archive.LastSyncAt), value.Archive.Messages, value.Archive.Senders, value.Archive.Since); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nAuth: %t\n", value.Auth.Authorized); err != nil {
		return err
	}
	return nil
}

func printSearchText(w io.Writer, value archive.SearchResult) error {
	if _, err := fmt.Fprintf(w, "Search %q: showing %d of %d.\n", value.Query, len(value.Results), value.TotalMatches); err != nil {
		return err
	}
	if value.Truncated {
		if _, err := io.WriteString(w, "More results exist; narrow with --after, --before or a more specific query.\n"); err != nil {
			return err
		}
	}
	for _, hit := range value.Results {
		if _, err := fmt.Fprintf(w, "%s  %s  %s  %s\n", hit.Time, hit.Who, hit.Ref, hit.Snippet); err != nil {
			return err
		}
	}
	return nil
}

func printOpenText(w io.Writer, value archive.OpenResult) error {
	if _, err := fmt.Fprintf(w, "Message %s\nTime: %s\nFrom: %s\nTo: %s\n",
		value.Ref, value.Time, senderText(value.Headers), emptyDash(value.Headers.ToAddress)); err != nil {
		return err
	}
	if value.Headers.CcAddress != "" {
		if _, err := fmt.Fprintf(w, "Cc: %s\n", value.Headers.CcAddress); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Subject: %s\n", emptyDash(value.Headers.Subject)); err != nil {
		return err
	}
	if len(value.Attachments) > 0 {
		if _, err := io.WriteString(w, "Attachments:\n"); err != nil {
			return err
		}
		for _, attachment := range value.Attachments {
			if _, err := fmt.Fprintf(w, "  %s (%s, %d bytes)\n", emptyDash(attachment.Filename), emptyDash(attachment.MIMEType), attachment.Size); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", value.Body); err != nil {
		return err
	}
	return nil
}

func printDoctorText(w io.Writer, value doctorOutput) error {
	if _, err := io.WriteString(w, "Doctor checks:\n"); err != nil {
		return err
	}
	for _, check := range value.Checks {
		if _, err := fmt.Fprintf(w, "  %s: %s", check.ID, check.State); err != nil {
			return err
		}
		if check.Message != "" {
			if _, err := fmt.Fprintf(w, " - %s", check.Message); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		if check.Remedy != "" {
			if _, err := fmt.Fprintf(w, "    Remedy: %s\n", check.Remedy); err != nil {
				return err
			}
		}
	}
	return nil
}

func printContactsText(w io.Writer, value control.ContactExport) error {
	for _, contact := range value.Contacts {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", contact.DisplayName, strings.Join(contact.PhoneNumbers, ",")); err != nil {
			return err
		}
	}
	return nil
}

func senderText(headers archive.MailHeaders) string {
	if headers.FromName != "" && headers.FromAddress != "" {
		return headers.FromName + " <" + headers.FromAddress + ">"
	}
	if headers.FromName != "" {
		return headers.FromName
	}
	return emptyDash(headers.FromAddress)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
