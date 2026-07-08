package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/openclaw/crawlkit"
	ckconfig "github.com/openclaw/crawlkit/config"
	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
)

type sourceStoreAccess int

const (
	sourceStoreNone sourceStoreAccess = iota
	sourceStoreOptional
	sourceStoreRead
	sourceStoreWrite
)

type resolvedSourcePaths struct {
	stateRoot string
	base      string
	paths     crawlkit.Paths
}

func (r *Runtime) withSourceRequest(source Source, verb string, storeAccess sourceStoreAccess, format ckoutput.Format, out io.Writer, fn func(context.Context, *crawlkit.Request) error) (err error) {
	if source.Crawler == nil {
		return errorsForMetadata(source)
	}
	paths, err := resolveSourcePaths(source)
	if err != nil {
		return err
	}
	runLog, err := cklog.NewRun(cklog.Options{
		StateRoot: paths.stateRoot,
		CrawlerID: source.ID,
		Command:   verb,
		Version:   Version,
		Verbosity: r.verbosity(),
		Stderr:    r.lockedStderr(),
	})
	if err != nil {
		return err
	}
	defer func() {
		if finishErr := runLog.Finish(err); err == nil && finishErr != nil {
			err = finishErr
		}
	}()

	if err := loadSourceConfig(source, paths); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()
	st, err := openSourceStore(ctx, paths.paths, storeAccess)
	if err != nil {
		return err
	}
	if st != nil {
		defer func() { _ = st.Close() }()
	}
	req := &crawlkit.Request{
		Store:  st,
		Paths:  paths.paths,
		Format: format,
		Out:    out,
		Log:    runLog,
		Progress: func(progress crawlkit.Progress) {
			_ = runLog.Info(progressLogEvent(progress.Phase), progressFields(progress))
		},
	}
	err = fn(ctx, req)
	if ctx.Err() != nil {
		return sourceTimeout(verb)
	}
	return err
}

func errorsForMetadata(source Source) error {
	if source.MetadataErr != nil {
		return source.MetadataErr
	}
	return fmt.Errorf("%s is not registered", source.ID)
}

func resolveSourcePaths(source Source) (resolvedSourcePaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return resolvedSourcePaths{}, err
	}
	stateRoot := filepath.Join(home, ".opentrawl")
	id := strings.TrimSpace(source.ID)
	if id == "" && source.Crawler != nil {
		id = strings.TrimSpace(source.Crawler.Info().ID)
	}
	if id == "" {
		return resolvedSourcePaths{}, fmt.Errorf("source id is required")
	}
	base := filepath.Join(stateRoot, id)
	paths := crawlkit.Paths{
		Archive: filepath.Join(base, id+".db"),
		Config:  filepath.Join(base, "config.toml"),
		Logs:    filepath.Join(base, "logs"),
	}
	if source.Crawler != nil {
		defaults := source.Crawler.Info().DefaultPaths
		if strings.TrimSpace(defaults.Archive) != "" {
			paths.Archive = ckconfig.ExpandHome(defaults.Archive)
		}
		if strings.TrimSpace(defaults.Config) != "" {
			paths.Config = ckconfig.ExpandHome(defaults.Config)
		}
		if strings.TrimSpace(defaults.Logs) != "" {
			paths.Logs = ckconfig.ExpandHome(defaults.Logs)
		}
	}
	return resolvedSourcePaths{
		stateRoot: stateRoot,
		base:      base,
		paths:     paths,
	}, nil
}

func loadSourceConfig(source Source, paths resolvedSourcePaths) error {
	info := source.Crawler.Info()
	if info.Config == nil {
		return nil
	}
	rv := reflect.ValueOf(info.Config)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return crawlkit.ConfigFieldError{Field: "config", Fix: "pass a pointer to the crawler config struct"}
	}
	exists, err := pathExists(paths.paths.Config)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	if exists {
		if err := ckconfig.LoadTOML(paths.paths.Config, info.Config); err != nil {
			return fmt.Errorf("load config: %w", err)
		}
	}
	if validator, ok := info.Config.(crawlkit.ConfigValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func openSourceStore(ctx context.Context, paths crawlkit.Paths, access sourceStoreAccess) (*ckstore.Store, error) {
	switch access {
	case sourceStoreNone:
		return nil, nil
	case sourceStoreOptional:
		exists, err := pathExists(paths.Archive)
		if err != nil {
			return nil, fmt.Errorf("stat archive: %w", err)
		}
		if !exists {
			return nil, nil
		}
		return ckstore.OpenReadOnly(ctx, paths.Archive)
	case sourceStoreRead:
		exists, err := pathExists(paths.Archive)
		if err != nil {
			return nil, fmt.Errorf("stat archive: %w", err)
		}
		if !exists {
			return nil, ckoutput.UsageError{Err: fmt.Errorf("archive does not exist at %s", paths.Archive)}
		}
		return ckstore.OpenReadOnly(ctx, paths.Archive)
	case sourceStoreWrite:
		return ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	default:
		return nil, fmt.Errorf("unknown store access %d", access)
	}
}

func sourceStoreFor(source Source, fallback sourceStoreAccess) sourceStoreAccess {
	if source.Crawler != nil && source.Crawler.Info().ID == "contacts" {
		return sourceStoreNone
	}
	return fallback
}

func pathExists(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func progressLogEvent(phase string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(phase)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case b.Len() > 0 && !lastUnderscore:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	event := strings.Trim(b.String(), "_")
	if event == "" || event[0] < 'a' || event[0] > 'z' {
		event = "progress"
	}
	if !strings.HasSuffix(event, "_progress") {
		event += "_progress"
	}
	return event
}

func progressFields(progress crawlkit.Progress) string {
	parts := []string{fmt.Sprintf("done=%d", progress.Done)}
	if progress.Total > 0 {
		parts = append(parts, fmt.Sprintf("total=%d", progress.Total))
	}
	if message := strings.Join(strings.Fields(progress.Message), " "); message != "" {
		parts = append(parts, "message="+logQuote(message))
	}
	return strings.Join(parts, " ")
}

func outputFormat(json bool) ckoutput.Format {
	if json {
		return ckoutput.JSON
	}
	return ckoutput.Text
}

func sourceErrorBody(err error) ckoutput.ErrorBody {
	return ckoutput.ErrorBodyFor(err)
}

func sourceTimeout(command string) sourceTimeoutError {
	return sourceTimeoutError{command: command}
}
