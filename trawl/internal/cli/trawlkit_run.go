package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/opentrawl/opentrawl/trawlkit"
)

var trawlkitRunMu sync.Mutex

type trawlkitRunOutput struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

func runTrawlkitCaptured(ctx context.Context, args []string, sources []trawlkit.Crawler) (trawlkitRunOutput, error) {
	trawlkitRunMu.Lock()
	defer trawlkitRunMu.Unlock()

	home, hasHome := os.LookupEnv("HOME")
	if hasHome {
		// Source database discovery still derives from HOME, independently of
		// OpenTrawl's state root. Unset then set collapses duplicate inherited
		// HOME entries so the current home is authoritative.
		_ = os.Unsetenv("HOME")
		if err := os.Setenv("HOME", home); err != nil {
			return trawlkitRunOutput{}, err
		}
	}
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return trawlkitRunOutput{}, fmt.Errorf("capture stdout: %w", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return trawlkitRunOutput{}, fmt.Errorf("capture stderr: %w", err)
	}

	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stdout, stdoutReader)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderr, stderrReader)
	}()

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()
	code := trawlkit.RunContext(ctx, args, sources)
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	wg.Wait()
	_ = stdoutReader.Close()
	_ = stderrReader.Close()

	return trawlkitRunOutput{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
		Code:   code,
	}, nil
}
