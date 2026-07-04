package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

type runtime struct {
	ctx          context.Context
	stdout       io.Writer
	stderr       io.Writer
	json         bool
	dbPath       string
	configPath   string
	verbosity    int
	logStateRoot string
	log          *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, rest, err := parseGlobalOptions(args)
	if err != nil {
		return usageErr(err)
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	if rest[0] == "--version" || rest[0] == "version" {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	r := &runtime{
		ctx:          ctx,
		stdout:       stdout,
		stderr:       stderr,
		json:         opts.json,
		dbPath:       opts.dbPath,
		configPath:   opts.configPath,
		verbosity:    opts.verbosity,
		logStateRoot: logStateRoot(opts.dbPath),
	}
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: r.logStateRoot,
		CrawlerID: "birdcrawl",
		Command:   logCommandName(rest[0]),
		Version:   version,
		Verbosity: opts.verbosity,
		Debug:     opts.verbosity > 1,
		Stderr:    stderr,
	})
	if err != nil {
		return err
	}
	r.log = run
	err = r.dispatch(rest)
	if err != nil {
		_ = r.log.Error(errorEventCode(err), err)
	}
	if finishErr := r.log.Finish(err); finishErr != nil {
		return errors.Join(err, finishErr)
	}
	return err
}

func (r *runtime) dispatch(args []string) error {
	if len(args) > 1 && hasHelpFlag(args[1:]) {
		printCommandUsage(r.stdout, args)
		return nil
	}
	switch args[0] {
	case "metadata":
		if r.json {
			return r.print(contractMetadata())
		}
		return r.print(controlManifest())
	case "status":
		return r.runStatus(args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "search":
		return r.runSearch(args[1:])
	case "open":
		return r.runOpen(args[1:])
	case "import":
		return r.runImport(args[1:])
	case "sync":
		return r.runSync(args[1:])
	case "stats":
		return r.runStats(args[1:])
	case "version":
		_, _ = io.WriteString(r.stdout, version+"\n")
		return nil
	default:
		return usageErr(fmt.Errorf("unknown command %q", args[0]))
	}
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Open(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.OpenReadOnly(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func logCommandName(command string) string {
	switch command {
	case "metadata", "status", "doctor", "search", "open", "import", "sync", "stats", "version":
		return command
	default:
		return "unknown"
	}
}

func errorEventCode(err error) string {
	if err == nil {
		return ""
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.event != "" {
		return codeErr.event
	}
	return "command_failed"
}

type globalOptions struct {
	json       bool
	dbPath     string
	configPath string
	verbosity  int
}

func parseGlobalOptions(args []string) (globalOptions, []string, error) {
	opts := globalOptions{dbPath: defaultDBPath()}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json" || arg == "-json":
			opts.json = true
		case arg == "-v" || arg == "--verbose":
			opts.verbosity = max(opts.verbosity, 1)
		case arg == "-vv":
			opts.verbosity = max(opts.verbosity, 2)
		case arg == "--db":
			if i+1 >= len(args) {
				return opts, nil, errors.New("--db takes a path")
			}
			i++
			opts.dbPath = args[i]
		case strings.HasPrefix(arg, "--db="):
			opts.dbPath = strings.TrimPrefix(arg, "--db=")
		case arg == "--config":
			if i+1 >= len(args) {
				return opts, nil, errors.New("--config takes a path")
			}
			i++
			opts.configPath = expandHome(args[i])
		case strings.HasPrefix(arg, "--config="):
			opts.configPath = expandHome(strings.TrimPrefix(arg, "--config="))
		default:
			rest = append(rest, arg)
		}
	}
	if opts.configPath == "" {
		opts.configPath = configPathForDB(opts.dbPath)
	}
	return opts, rest, nil
}
