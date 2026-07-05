package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/backup"
	"github.com/openclaw/wacrawl/internal/store"
)

// printManifestText renders `wacrawl metadata`'s human card. It used to
// fall through print's default case and dump the raw JSON manifest even
// in human mode — the only crawler in the fleet without a real card
// (TRAWL-125). The shape matches the other crawlers' cards.
func printManifestText(w io.Writer, value control.Manifest) error {
	return render.WriteCard(w, render.Card{
		Title: value.DisplayName + " crawler",
		Fields: []render.CardField{
			{Label: "ID", Value: value.ID},
			{Label: "Version", Value: value.Version},
			{Label: "Contract", Value: fmt.Sprintf("v%d", value.ContractVersion)},
			{Label: "Database", Value: value.Paths.DefaultDatabase},
			{Label: "Logs", Value: value.Paths.DefaultLogs},
		},
		Body:  value.Description,
		Hints: []string{"JSON: wacrawl metadata --json"},
	})
}

func (a *app) print(value any) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case control.Manifest:
		return printManifestText(a.stdout, v)
	case store.ImportStats:
		_, err := fmt.Fprintf(a.stdout, "source=%s\ndb=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\nmedia_copied=%d\nmedia_missing=%d\n",
			v.SourcePath, v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, v.MediaCopied, v.MediaMissing)
		return err
	case store.Status:
		return a.printStatus(v, logTailEnvelope{})
	case chatsEnvelope:
		return a.printChats(v)
	case messageListOutput:
		return a.printMessages(v)
	case control.ContactExport:
		return a.printContactExport(v)
	case whoEnvelope:
		return a.printWho(v)
	case searchEnvelope:
		return a.printSearch(v)
	case openEnvelope:
		return a.printOpen(v)
	case doctorEnvelope:
		return a.printDoctor(v)
	case backup.Result:
		_, err := fmt.Fprintf(a.stdout, "repo=%s\nchanged=%t\nencrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\n", v.Repo, v.Changed, v.Encrypted, v.Shards, v.Messages, v.MediaFiles)
		if err == nil && v.Ref != "" {
			_, err = fmt.Fprintf(a.stdout, "ref=%s\n", v.Ref)
		}
		if err == nil && v.Tag != "" {
			_, err = fmt.Fprintf(a.stdout, "tag=%s\n", v.Tag)
		}
		return err
	case []backup.Snapshot:
		if len(v) == 0 {
			_, err := fmt.Fprintln(a.stdout, "No backup snapshots found.")
			return err
		}
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "REF\tEXPORTED\tMESSAGES\tMEDIA\tSHARDS\tTAGS")
		for _, snapshot := range v {
			ref := snapshot.Ref
			if len(ref) > 12 {
				ref = ref[:12]
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n", ref, formatTime(snapshot.Exported), snapshot.Counts.Messages, snapshot.Counts.MediaFiles, snapshot.Shards, strings.Join(snapshot.Tags, ","))
		}
		return tw.Flush()
	case backup.Manifest:
		_, err := fmt.Fprintf(a.stdout, "encrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\nexported=%s\n", v.Encrypted, len(v.Shards), v.Counts.Messages, len(v.Files), formatTime(v.Exported))
		return err
	default:
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
}
