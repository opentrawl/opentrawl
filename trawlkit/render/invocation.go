package render

import (
	"io"
	"strings"
)

const defaultTrawlInvocationDisplay = "./trawl"

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

func WithTrawlInvocationDisplay(writer io.Writer, trawlInvocationDisplay string) io.Writer {
	trawlInvocationDisplay = strings.TrimSpace(trawlInvocationDisplay)
	if trawlInvocationDisplay == "" {
		trawlInvocationDisplay = defaultTrawlInvocationDisplay
	}
	return trawlInvocationDisplayWriter{
		Writer:                 writer,
		trawlInvocationDisplay: trawlInvocationDisplay,
	}
}

func TrawlInvocationDisplay(writer io.Writer) string {
	if writerWithTrawlInvocationDisplay, ok := writer.(interface{ TrawlInvocationDisplay() string }); ok {
		if trawlInvocationDisplay := strings.TrimSpace(writerWithTrawlInvocationDisplay.TrawlInvocationDisplay()); trawlInvocationDisplay != "" {
			return trawlInvocationDisplay
		}
	}
	return defaultTrawlInvocationDisplay
}
