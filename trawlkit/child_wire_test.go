package trawlkit

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestReadChildFrameRejectsMalformedProto(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte{0xff}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(len(payload)))
	buf.Write(prefix[:n])
	buf.Write(payload)

	_, err := readChildFrame(bufio.NewReader(&buf))
	if err == nil || !strings.Contains(err.Error(), "decode child frame") {
		t.Fatalf("readChildFrame err = %v, want decode child frame error", err)
	}
}

func TestReadChildFrameRejectsOversizedFrameBeforeReadingBody(t *testing.T) {
	var buf bytes.Buffer
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], (16<<20)+1)
	buf.Write(prefix[:n])

	_, err := readChildFrame(bufio.NewReader(&buf))
	if err == nil || !strings.Contains(err.Error(), "decode child frame: protobuf frame is 16777217 bytes; maximum is 16777216") {
		t.Fatalf("readChildFrame err = %v, want maximum-size decode error", err)
	}
}
