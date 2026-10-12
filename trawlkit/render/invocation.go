package render

import (
	"io"
	"strings"
)

const defaultTrawlInvocationDisplay = "./trawl"

// trawlInvocationDisplayWriter carries how this run spells trawl, so every
// next command printed through the writer copies and pastes as it was run.
type trawlInvocationDisplayWriter struct {
	io.Writer
	trawlInvocationDisplay string
}

func (writer trawlInvocationDisplayWriter) TrawlInvocationDisplay() string {
	return writer.trawlInvocationDisplay
}

func (writer trawlInvocationDisplayWriter) UnwrapWriter() io.Writer {
	return writer.Writer
}

func withTrawlInvocationDisplay(writer io.Writer, trawlInvocationDisplay string) io.Writer {
	trawlInvocationDisplay = strings.TrimSpace(trawlInvocationDisplay)
	if trawlInvocationDisplay == "" {
		trawlInvocationDisplay = defaultTrawlInvocationDisplay
	}
	return trawlInvocationDisplayWriter{
		Writer:                 writer,
		trawlInvocationDisplay: trawlInvocationDisplay,
	}
}

func trawlInvocationDisplayForWriter(writer io.Writer) string {
	for writer != nil {
		if writerWithTrawlInvocationDisplay, ok := writer.(interface{ TrawlInvocationDisplay() string }); ok {
			if trawlInvocationDisplay := strings.TrimSpace(writerWithTrawlInvocationDisplay.TrawlInvocationDisplay()); trawlInvocationDisplay != "" {
				return trawlInvocationDisplay
			}
		}
		wrappedWriter, ok := writer.(writerUnwrapper)
		if !ok {
			break
		}
		writer = wrappedWriter.UnwrapWriter()
	}
	return defaultTrawlInvocationDisplay
}
