package prototransport

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	worker "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker"
)

func TestDelimitedRoundTrip(t *testing.T) {
	want := &worker.Log{Text: "synthetic log line"}
	var stream bytes.Buffer
	if err := WriteDelimited(&stream, want); err != nil {
		t.Fatal(err)
	}
	var got worker.Log
	if err := ReadDelimited(bufio.NewReader(&stream), &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != want.Text {
		t.Fatalf("text = %q, want %q", got.Text, want.Text)
	}
}

func TestDelimitedCleanEOF(t *testing.T) {
	var got worker.Log
	err := ReadDelimited(bufio.NewReader(bytes.NewReader(nil)), &got)
	if err != io.EOF {
		t.Fatalf("error = %v, want io.EOF", err)
	}
}

func TestDelimitedRejectsOversizedFrameBeforeAllocating(t *testing.T) {
	var stream bytes.Buffer
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], maxFrameBytes+1)
	stream.Write(prefix[:n])
	var got worker.Log
	err := ReadDelimited(bufio.NewReader(&stream), &got)
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("error = %v, want maximum-size error", err)
	}
}
