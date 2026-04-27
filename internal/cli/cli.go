package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/steipete/wacrawl/internal/backup"
	"github.com/steipete/wacrawl/internal/store"
	"github.com/steipete/wacrawl/internal/whatsappdb"
)

type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string { return e.err.Error() }

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

type app struct {
	stdout     io.Writer
	stderr     io.Writer
	json       bool
	dbPath     string
	source     string
	syncMode   archiveSyncMode
	syncMaxAge time.Duration
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	global := flag.NewFlagSet("wacrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	syncFlag := global.String("sync", string(archiveSyncAuto), "")
	syncMaxAge := global.Duration("sync-max-age", 15*time.Minute, "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		return usageErr(err)
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	syncMode, err := parseArchiveSyncMode(*syncFlag)
	if err != nil {
		return usageErr(err)
	}
	a := &app{stdout: stdout, stderr: stderr, json: *jsonOut, dbPath: *dbPath, source: *source, syncMode: syncMode, syncMaxAge: *syncMaxAge}
	rest := global.Args()
	if len(rest) == 0 || rest[0] == "help" {
		printUsage(stdout)
		return nil
	}
	switch rest[0] {
	case "doctor":
		return a.runDoctor(ctx, rest[1:])
	case "import", "sync":
		return a.runImport(ctx, rest[0], rest[1:])
	case "status":
		return a.runStatus(ctx, rest[1:])
	case "chats":
		return a.runChats(ctx, rest[1:])
	case "messages":
		return a.runMessages(ctx, rest[1:])
	case "search":
		return a.runSearch(ctx, rest[1:])
	case "backup":
		return a.runBackup(ctx, rest[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", rest[0]))
	}
}

func (a *app) withStore(ctx context.Context, fn func(*store.Store) error) error {
	st, err := store.Open(ctx, a.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (a *app) withArchiveStore(ctx context.Context, fn func(*store.Store) error) error {
	return a.withStore(ctx, func(st *store.Store) error {
		if err := a.syncArchive(ctx, st); err != nil {
			return err
		}
		return fn(st)
	})
}

func (a *app) runStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes flags only"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		status, err := st.Status(ctx)
		if err != nil {
			return err
		}
		return a.print(status)
	})
}

func (a *app) runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("doctor takes flags only"))
	}
	src, err := whatsappdb.Discover(ctx, *source)
	if err != nil {
		return err
	}
	report := map[string]any{
		"desktop":  src,
		"db_path":  a.dbPath,
		"readonly": true,
	}
	return a.print(report)
}

func (a *app) runImport(ctx context.Context, command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(fmt.Errorf("%s takes flags only", command))
	}
	return a.withStore(ctx, func(st *store.Store) error {
		stats, err := whatsappdb.Import(ctx, st, *source)
		if err != nil {
			return err
		}
		return a.print(stats)
	})
}

func (a *app) runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		chats, err := st.ListChats(ctx, *limit)
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}

func (a *app) runMessages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindMessageFlags(fs)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	resolved, err := filter.resolve()
	if err != nil {
		return usageErr(err)
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		msgs, err := st.Messages(ctx, resolved)
		if err != nil {
			return err
		}
		return a.print(msgs)
	})
}

func (a *app) runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindMessageFlags(fs)
	flagArgs, query, err := splitSearchArgs(args)
	if err != nil {
		return usageErr(err)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return usageErr(err)
	}
	resolved, err := filter.resolve()
	if err != nil {
		return usageErr(err)
	}
	resolved.Query = query
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		msgs, err := st.Search(ctx, resolved)
		if err != nil {
			return err
		}
		return a.print(msgs)
	})
}

func splitSearchArgs(args []string) ([]string, string, error) {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if searchFlagNeedsValue(arg) && !strings.Contains(arg, "=") {
				next := i + 1
				if next >= len(args) {
					return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
				}
				flags = append(flags, args[next]) // #nosec G602 -- next is checked against len(args) above.
				i = next
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 {
		return nil, "", errors.New("search requires exactly one query")
	}
	return flags, positionals[0], nil
}

func searchFlagNeedsValue(arg string) bool {
	name := strings.TrimPrefix(arg, "-")
	name = strings.TrimPrefix(name, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		name = before
	}
	switch name {
	case "chat", "sender", "limit", "after", "before":
		return true
	default:
		return false
	}
}

type messageFlags struct {
	chat     *string
	sender   *string
	limit    *int
	after    *string
	before   *string
	fromMe   *bool
	fromThem *bool
	hasMedia *bool
	asc      *bool
}

func bindMessageFlags(fs *flag.FlagSet) messageFlags {
	return messageFlags{
		chat:     fs.String("chat", "", ""),
		sender:   fs.String("sender", "", ""),
		limit:    fs.Int("limit", 50, ""),
		after:    fs.String("after", "", ""),
		before:   fs.String("before", "", ""),
		fromMe:   fs.Bool("from-me", false, ""),
		fromThem: fs.Bool("from-them", false, ""),
		hasMedia: fs.Bool("has-media", false, ""),
		asc:      fs.Bool("asc", false, ""),
	}
}

func (f messageFlags) resolve() (store.MessageFilter, error) {
	if *f.fromMe && *f.fromThem {
		return store.MessageFilter{}, errors.New("--from-me and --from-them are mutually exclusive")
	}
	out := store.MessageFilter{
		ChatJID:  *f.chat,
		Sender:   *f.sender,
		Limit:    *f.limit,
		HasMedia: *f.hasMedia,
		Asc:      *f.asc,
	}
	if *f.fromMe {
		v := true
		out.FromMe = &v
	}
	if *f.fromThem {
		v := false
		out.FromMe = &v
	}
	if strings.TrimSpace(*f.after) != "" {
		t, err := parseTime(*f.after)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.After = &t
	}
	if strings.TrimSpace(*f.before) != "" {
		t, err := parseTime(*f.before)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.Before = &t
	}
	return out, nil
}

func (a *app) print(value any) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case store.ImportStats:
		_, err := fmt.Fprintf(a.stdout, "source=%s\ndb=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\n",
			v.SourcePath, v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages)
		return err
	case store.Status:
		_, err := fmt.Fprintf(a.stdout, "db=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\noldest=%s\nnewest=%s\nlast_import=%s\nsource=%s\n",
			v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, formatTime(v.OldestMessage), formatTime(v.NewestMessage), formatTime(v.LastImportAt), v.LastSource)
		return err
	case []store.Chat:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "LAST\tKIND\tMESSAGES\tJID\tNAME")
		for _, c := range v {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", formatTime(c.LastMessageAt), c.Kind, c.MessageCount, c.JID, c.Name)
		}
		return tw.Flush()
	case []store.Message:
		for _, m := range v {
			body := firstNonEmpty(m.Snippet, m.Text, m.MediaTitle, m.MessageType)
			if _, err := fmt.Fprintf(a.stdout, "[%s] %s / %s / %s\n%s\n\n", formatTime(m.Timestamp), m.ChatName, firstNonEmpty(m.SenderName, m.SenderJID), m.MessageID, body); err != nil {
				return err
			}
		}
		return nil
	case backup.Result:
		_, err := fmt.Fprintf(a.stdout, "repo=%s\nchanged=%t\nencrypted=%t\nshards=%d\nmessages=%d\n", v.Repo, v.Changed, v.Encrypted, v.Shards, v.Messages)
		return err
	case backup.Manifest:
		_, err := fmt.Fprintf(a.stdout, "encrypted=%t\nshards=%d\nmessages=%d\nexported=%s\n", v.Encrypted, len(v.Shards), v.Counts.Messages, formatTime(v.Exported))
		return err
	default:
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `wacrawl reads local WhatsApp Desktop data into a readonly archive.

Usage:
  wacrawl [--db PATH] [--source PATH] [--json] [--sync auto|always|never] <command> [args]
  wacrawl --version

Commands:
  doctor      Inspect WhatsApp Desktop source and archive paths.
  import      Snapshot WhatsApp Desktop SQLite data into the archive.
  sync        Alias for import.
  status      Show archive status.
  chats       List chats.
  messages    List archived messages.
  search      Search archived messages.
  backup      Init, push, pull, or inspect encrypted Git backups.

Options:
  --db PATH                 Archive database path.
  --source PATH             WhatsApp Desktop source path.
  --sync auto|always|never  Read-time sync policy. Default: auto.
  --sync-max-age DURATION   Staleness window for --sync auto. Default: 15m.
  --json                    Emit JSON output.
  --version                 Print the CLI version.
`)
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "wacrawl.db"
	}
	return filepath.Join(home, ".wacrawl", "wacrawl.db")
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err}
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty time")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q", value)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
