// Package render writes every word OpenTrawl shows a person or an agent:
// records, help, next commands and failures. Commands, trawlers and the CLI
// hand it typed values and never print or format text themselves; lint in
// .golangci.yml rejects a print call anywhere else. If a command needs output
// this package cannot make, change this package for every command.
package render

import (
	"io"
	"sync"
)

// TrawlCommandOutput is the only way a trawl command reaches the person or
// agent who ran it. A command hands it typed values; the library decides
// every word, column, time, link and next command. Records and help go to
// standard output; failures go to standard error.
type TrawlCommandOutput struct {
	standardOutput io.Writer
	standardError  io.Writer
}

// NewTrawlCommandOutput wraps the process streams once, at the program entry.
// trawlInvocationDisplay is how next commands spell trawl, such as "trawl" or
// "./trawl".
func NewTrawlCommandOutput(
	standardOutput io.Writer,
	standardError io.Writer,
	trawlInvocationDisplay string,
) TrawlCommandOutput {
	standardErrorLock := &sync.Mutex{}
	return TrawlCommandOutput{
		standardOutput: withTrawlInvocationDisplay(standardOutput, trawlInvocationDisplay),
		standardError: withTrawlInvocationDisplay(
			standardErrorWriterSharedWithDiagnostics{writer: standardError, lock: standardErrorLock},
			trawlInvocationDisplay,
		),
	}
}

// DiagnosticLogStream is standard error for the run log's progress and
// debug lines, which trawlkit/log writes when a person asks for -v.
func (output TrawlCommandOutput) DiagnosticLogStream() io.Writer {
	return output.standardError
}

// standardErrorWriterSharedWithDiagnostics serialises writes, because
// trawlers report progress on standard error from several goroutines.
type standardErrorWriterSharedWithDiagnostics struct {
	writer io.Writer
	lock   *sync.Mutex
}

func (writer standardErrorWriterSharedWithDiagnostics) Write(text []byte) (int, error) {
	writer.lock.Lock()
	defer writer.lock.Unlock()
	return writer.writer.Write(text)
}

func (writer standardErrorWriterSharedWithDiagnostics) UnwrapWriter() io.Writer {
	return writer.writer
}

// writeLine is the primitive every renderer uses to write one line.
func writeLine(writer io.Writer, line string) error {
	_, err := io.WriteString(writer, line+"\n")
	return err
}

func writeLines(writer io.Writer, lines []string) error {
	for _, line := range lines {
		if err := writeLine(writer, line); err != nil {
			return err
		}
	}
	return nil
}
