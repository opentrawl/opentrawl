package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/openclaw/wacrawl/internal/backup"
	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) print(value any) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case store.ImportStats:
		_, err := fmt.Fprintf(a.stdout, "source=%s\ndb=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\nmedia_copied=%d\nmedia_missing=%d\n",
			v.SourcePath, v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, v.MediaCopied, v.MediaMissing)
		return err
	case store.Status:
		return a.printStatus(v, logTailEnvelope{})
	case []store.Chat:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "LAST\tKIND\tUNREAD\tMESSAGES\tJID\tNAME")
		for _, c := range v {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", formatTime(c.LastMessageAt), c.Kind, c.UnreadCount, c.MessageCount, c.JID, c.Name)
		}
		return tw.Flush()
	case []store.Message:
		return a.printMessages(v, false, 0)
	case messageListOutput:
		return a.printMessages(v.Messages, v.Truncated, v.Limit)
	case whoEnvelope:
		return a.printWho(v)
	case searchEnvelope:
		return a.printSearch(v)
	case openEnvelope:
		return a.printOpen(v)
	case doctorEnvelope:
		return a.printDoctor(v)
	case sqlQueryResult:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(v.columns, "\t"))
		for _, row := range v.rows {
			values := make([]string, 0, len(v.columns))
			for _, col := range v.columns {
				values = append(values, formatSQLValue(row[col]))
			}
			_, _ = fmt.Fprintln(tw, strings.Join(values, "\t"))
		}
		return tw.Flush()
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
