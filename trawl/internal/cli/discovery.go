package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const crawlerCommandTimeout = 30 * time.Second

var builtInBinaries = []string{
	"imsgcrawl",
	"telecrawl",
	"wacrawl",
	"clawdex",
	"photoscrawl",
	"gogcrawl",
	"calcrawl",
}

type Source struct {
	ID           string
	Binary       string
	Path         string
	DisplayName  string
	Capabilities []string
	MetadataErr  error
}

type dropInManifest struct {
	ID     string `json:"id"`
	Binary string `json:"binary"`
}

func discoverCrawlers(ctx context.Context, appsDir string) []Source {
	var sources []Source
	for _, binary := range registryBinaries(appsDir) {
		path, err := exec.LookPath(binary)
		if err != nil {
			continue
		}
		source := Source{
			ID:     binary,
			Binary: binary,
			Path:   path,
		}
		metadata, err := probeMetadata(ctx, path)
		if err != nil {
			source.MetadataErr = err
			sources = append(sources, source)
			continue
		}
		source.ID = metadata.ID
		source.DisplayName = metadata.DisplayName
		source.Capabilities = metadata.Capabilities
		sources = append(sources, source)
	}
	return sources
}

func defaultAppsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".trawl", "apps")
}

func registryBinaries(appsDir string) []string {
	seen := map[string]bool{}
	var binaries []string
	add := func(binary string) {
		binary = strings.TrimSpace(binary)
		if binary == "" || seen[binary] {
			return
		}
		seen[binary] = true
		binaries = append(binaries, binary)
	}
	for _, binary := range builtInBinaries {
		add(binary)
	}
	for _, manifest := range readDropInManifests(appsDir) {
		add(manifest.Binary)
	}
	return binaries
}

func readDropInManifests(appsDir string) []dropInManifest {
	if appsDir == "" {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(appsDir, "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	var manifests []dropInManifest
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		var manifest dropInManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Binary) == "" {
			continue
		}
		manifests = append(manifests, manifest)
	}
	return manifests
}

func probeMetadata(ctx context.Context, path string) (Metadata, error) {
	data, err := runCrawlerJSON(ctx, path, "metadata")
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	if err := decodeContractJSON(data, &metadata); err != nil {
		return Metadata{}, err
	}
	if strings.TrimSpace(metadata.ID) == "" {
		return Metadata{}, errors.New("metadata id is empty")
	}
	metadata.ID = strings.TrimSpace(metadata.ID)
	return metadata, nil
}

func runCrawlerJSON(ctx context.Context, path, verb string) ([]byte, error) {
	return runCrawlerCommandWithTimeout(ctx, path, crawlerCommandTimeout, verb, "--json")
}

func runCrawlerJSONWithArgs(ctx context.Context, path, verb string, args ...string) ([]byte, error) {
	commandArgs := append([]string{verb}, args...)
	commandArgs = append(commandArgs, "--json")
	return runCrawlerCommandWithTimeout(ctx, path, crawlerCommandTimeout, commandArgs...)
}

func runCrawlerCommandJSON(ctx context.Context, path string, args ...string) ([]byte, error) {
	return runCrawlerCommandWithTimeout(ctx, path, crawlerCommandTimeout, args...)
}

func runCrawlerJSONNoTimeout(ctx context.Context, path, verb string, args ...string) ([]byte, error) {
	commandArgs := append([]string{verb}, args...)
	commandArgs = append(commandArgs, "--json")
	return runCrawlerCommand(ctx, path, commandArgs...)
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
