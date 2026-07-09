// Package projection turns an Apple Notes body (a gzip/zlib-compressed
// NoteStoreProto protobuf blob, "zdata") into a markdown string. It is one
// deep module with one job: zdata bytes in, markdown out. Tables are the sole
// exception — their cell content lives in a separate blob keyed by attachment
// UUID, so table decoding takes a resolver as a second input (see table.go).
package projection

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// TableResolver fetches the gzip'd MergableDataProto blob for a table
// attachment, keyed by the attachment UUID. ok is false when the bytes were
// never captured (every table in an archive written before TRAWL-210).
type TableResolver func(attachmentUUID string) (zdata []byte, ok bool)

// Field numbers for the note-body message tree (see notestore.proto).
const (
	fieldDocument     = 2 // NoteStoreProto.document
	fieldNote         = 3 // Document.note
	fieldNoteText     = 2 // Note.note_text
	fieldAttributeRun = 5 // Note.attribute_run
)

// DecodeMarkdown decodes a note body to markdown. resolve may be nil, in
// which case every table renders as its "not captured" marker.
func DecodeMarkdown(zdata []byte, resolve TableResolver) (string, error) {
	note, err := decodeNote(zdata)
	if err != nil {
		return "", err
	}
	return renderNote(note, resolve), nil
}

// TableAttachmentUUIDs returns the attachment UUID of every table embedded in
// a note body. Sync uses it to know which companion blobs to capture.
func TableAttachmentUUIDs(zdata []byte) ([]string, error) {
	note, err := decodeNote(zdata)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, run := range note.runs {
		if run.attachment != nil && run.attachment.typeUTI == utiTable {
			if id := run.attachment.identifier; id != "" {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

type decodedNote struct {
	text string
	runs []attributeRun
}

func decodeNote(zdata []byte) (decodedNote, error) {
	body, err := Inflate(zdata)
	if err != nil {
		return decodedNote{}, err
	}
	root, err := parse(body)
	if err != nil {
		return decodedNote{}, fmt.Errorf("note store: %w", err)
	}
	document, ok, err := root.child(fieldDocument)
	if err != nil {
		return decodedNote{}, fmt.Errorf("document field: %w", err)
	}
	if !ok {
		return decodedNote{}, errors.New("note body has no document")
	}
	noteMsg, ok, err := document.child(fieldNote)
	if err != nil {
		return decodedNote{}, fmt.Errorf("note field: %w", err)
	}
	if !ok {
		return decodedNote{}, errors.New("document has no note")
	}
	text, _ := noteMsg.str(fieldNoteText)
	runs, err := parseAttributeRuns(noteMsg.repeated(fieldAttributeRun))
	if err != nil {
		return decodedNote{}, err
	}
	return decodedNote{text: text, runs: runs}, nil
}

// Inflate decompresses a note body. Apple stores current bodies as gzip
// (and, historically, raw zlib). Bodies that begin with "bplist00" are the
// pre-protobuf binary-plist format and are reported as such so the archive
// can record a precise, honest unsupported_reason.
func Inflate(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, errors.New("note body is too short")
	}
	switch {
	case data[0] == 0x1f && data[1] == 0x8b:
		return readAll(gzipReader(data))
	case data[0] == 0x78:
		return readAll(zlibReader(data))
	case bytes.HasPrefix(data, []byte("bplist00")):
		return nil, errors.New("zdata is a legacy binary-plist note body (pre-protobuf Apple Notes format), not decodable by this markdown projector")
	default:
		return nil, errors.New("note body is not gzip or zlib data")
	}
}

func gzipReader(data []byte) (io.ReadCloser, error) {
	return gzip.NewReader(bytes.NewReader(data))
}

func zlibReader(data []byte) (io.ReadCloser, error) {
	return zlib.NewReader(bytes.NewReader(data))
}

func readAll(r io.ReadCloser, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}
