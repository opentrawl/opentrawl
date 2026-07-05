package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/registry"
)

const crawlerCommandTimeout = 30 * time.Second

// Source is one installed crawler as trawl uses it: the addressable id,
// the surface name a person says out loud, the verbs it exposes, and the
// resolved binary path trawl spawns. It is a flat projection of the
// crawlkit manifest — the registry is the one discoverer.
type Source struct {
	ID           string
	Binary       string
	Path         string
	DisplayName  string
	Description  string
	Capabilities []string
	LogDir       string
	Commands     map[string]control.Command
	MetadataErr  error
}

// discoverCrawlers asks the registry which crawlers are installed and
// projects each manifest into a Source. A crawler whose metadata did not
// parse keeps its binary name as the id and carries the probe error so
// status and doctor can surface it.
func discoverCrawlers(ctx context.Context) []Source {
	crawlers := registry.Discover(ctx)
	sources := make([]Source, 0, len(crawlers))
	for _, crawler := range crawlers {
		if crawler.Err != nil {
			sources = append(sources, Source{
				ID:          crawler.Name,
				Binary:      crawler.Name,
				Path:        crawler.Path,
				MetadataErr: crawler.Err,
			})
			continue
		}
		m := crawler.Manifest
		sources = append(sources, Source{
			ID:           m.ID,
			Binary:       firstNonEmpty(m.Binary.Name, crawler.Name),
			Path:         crawler.Path,
			DisplayName:  m.DisplayName,
			Description:  m.Description,
			Capabilities: m.Capabilities,
			LogDir:       m.Paths.DefaultLogs,
			Commands:     m.Commands,
		})
	}
	return sources
}

func runCrawlerJSONWithArgs(ctx context.Context, path, verb string, args ...string) ([]byte, error) {
	commandArgs := append([]string{verb}, args...)
	commandArgs = append(commandArgs, "--json")
	return runCrawlerCommandWithTimeout(ctx, path, crawlerCommandTimeout, commandArgs...)
}

func runCrawlerCommandWithTimeout(ctx context.Context, path string, timeout time.Duration, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	data, err := runCrawlerCommand(commandCtx, path, args...)
	if err != nil {
		if commandCtx.Err() != nil {
			return nil, fmt.Errorf("%s timed out", strings.Join(args, " "))
		}
		return data, err
	}
	return data, nil
}

func runCrawlerCommandPassThroughWithTimeout(ctx context.Context, path string, timeout time.Duration, stdout, stderr io.Writer, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := runCrawlerCommandPassThrough(commandCtx, path, stdout, stderr, args...); err != nil {
		if commandCtx.Err() != nil {
			return fmt.Errorf("%s timed out", strings.Join(args, " "))
		}
		return err
	}
	return nil
}

func runCrawlerCommandPassThrough(ctx context.Context, path string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code := exit.ExitCode()
			if code < 0 {
				code = 1
			}
			return exitErr{code: code}
		}
		return crawlerCommandError{
			command: strings.Join(args, " "),
			err:     err,
		}
	}
	return nil
}

func runCrawlerCommand(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), crawlerCommandError{
			command: strings.Join(args, " "),
			err:     err,
		}
	}
	return stdout.Bytes(), nil
}

type crawlerCommandError struct {
	command string
	err     error
}

func (e crawlerCommandError) Error() string {
	return fmt.Sprintf("%s failed", e.command)
}

func (e crawlerCommandError) Unwrap() error {
	return e.err
}
