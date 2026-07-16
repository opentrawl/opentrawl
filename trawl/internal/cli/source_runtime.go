package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckconfig "github.com/opentrawl/opentrawl/trawlkit/config"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

type sourceStoreAccess int

const (
	sourceStoreNone sourceStoreAccess = iota
	sourceStoreOptional
	sourceStoreRead
)

type resolvedSourcePaths struct {
	stateRoot string
	base      string
	paths     trawlkit.Paths
}

func (r *Runtime) withSourceRequest(source Source, verb string, storeAccess sourceStoreAccess, format ckoutput.Format, out io.Writer, fn func(context.Context, *trawlkit.Request) error) error {
	err := r.withSourceRequestContext(r.ctx, source, verb, storeAccess, format, out, fn)
	if errors.Is(err, context.DeadlineExceeded) {
		return sourceTimeout(verb)
	}
	return err
}

func (r *Runtime) withSourceRequestContext(parent context.Context, source Source, verb string, storeAccess sourceStoreAccess, format ckoutput.Format, out io.Writer, fn func(context.Context, *trawlkit.Request) error) (err error) {
	if err := parent.Err(); err != nil {
		return err
	}
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
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	if storeAccess == sourceStoreOptional || storeAccess == sourceStoreRead {
		if preparer, ok := source.Crawler.(trawlkit.ReadArchivePreparer); ok {
			started := time.Now()
			if err := preparer.PrepareReadArchive(ctx, paths.paths.Archive); err != nil {
				_ = runLog.Info("archive_prepare_read", fmt.Sprintf("duration_ms=%d", time.Since(started).Milliseconds()))
				return err
			}
			_ = runLog.Info("archive_prepare_read", fmt.Sprintf("duration_ms=%d", time.Since(started).Milliseconds()))
		}
	}
	st, err := openSourceStore(ctx, paths.paths, storeAccess)
	if err != nil {
		return err
	}
	if st != nil {
		defer func() { _ = st.Close() }()
	}
	req := &trawlkit.Request{
		Store:  st,
		Paths:  paths.paths,
		Format: format,
		Out:    out,
		Log:    runLog,
		Progress: func(progress trawlkit.Progress) {
			_ = runLog.Info(progressLogEvent(progress.Phase), progressFields(progress))
		},
	}
	err = fn(ctx, req)
	if ctx.Err() != nil {
		return ctx.Err()
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
	paths := trawlkit.Paths{
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
		return trawlkit.ConfigFieldError{Field: "config", Fix: "pass a pointer to the crawler config struct"}
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
	if validator, ok := info.Config.(trawlkit.ConfigValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func openSourceStore(ctx context.Context, paths trawlkit.Paths, access sourceStoreAccess) (*ckstore.Store, error) {
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
			return nil, trawlkit.NewMissingArchiveError(paths.Archive)
		}
		return ckstore.OpenReadOnly(ctx, paths.Archive)
	default:
		return nil, fmt.Errorf("unknown store access %d", access)
	}
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

func progressFields(progress trawlkit.Progress) string {
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
