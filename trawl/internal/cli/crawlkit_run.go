package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/openclaw/crawlkit"
)

var crawlkitRunMu sync.Mutex

type crawlkitRunOutput struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

func runCrawlkitCaptured(args []string, sources []crawlkit.Crawler) (crawlkitRunOutput, error) {
	crawlkitRunMu.Lock()
	defer crawlkitRunMu.Unlock()

	home, hasHome := os.LookupEnv("HOME")
	if hasHome {
		// Captured crawlers derive ~/.opentrawl from HOME. Unset then set
		// collapses duplicate inherited HOME entries so the synthetic test
		// home replaces the parent value instead of sitting behind it.
		_ = os.Unsetenv("HOME")
		if err := os.Setenv("HOME", home); err != nil {
			return crawlkitRunOutput{}, err
		}
	}
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return crawlkitRunOutput{}, fmt.Errorf("capture stdout: %w", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return crawlkitRunOutput{}, fmt.Errorf("capture stderr: %w", err)
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
	code := crawlkit.Run(args, sources)
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	wg.Wait()
	_ = stdoutReader.Close()
	_ = stderrReader.Close()

	return crawlkitRunOutput{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
		Code:   code,
	}, nil
}
