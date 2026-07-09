package projection

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"testing"
)

// --- hand-rolled protobuf writer, mirroring the reader in this package ---

func pbVarintField(field int, value uint64) []byte {
	var key [10]byte
	n := binary.PutUvarint(key[:], uint64(field<<3))
	var val [10]byte
	m := binary.PutUvarint(val[:], value)
	return append(append([]byte{}, key[:n]...), val[:m]...)
}

func pbInt32Field(field int, value int32) []byte {
	return pbVarintField(field, uint64(int64(value)))
}

func pbBytesField(field int, data []byte) []byte {
	var key [10]byte
	n := binary.PutUvarint(key[:], uint64(field<<3|2))
	var length [10]byte
	m := binary.PutUvarint(length[:], uint64(len(data)))
	out := append(append([]byte{}, key[:n]...), length[:m]...)
	return append(out, data...)
}

func pbStringField(field int, s string) []byte {
	return pbBytesField(field, []byte(s))
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// --- fixture builders for a note body ---

type runSpec struct {
	length     int
	styleType  int32
	indent     int
	hasStyle   bool
	checkbox   bool
	checked    bool
	attachUTI  string
	attachUUID string
}

func checklist(done bool) []byte {
	d := 0
	if done {
		d = 1
	}
	return pbBytesField(fieldChecklist, concat(
		pbBytesField(1, []byte{0x11, 0x22}), // uuid (required)
		pbVarintField(fieldChecklistDone, uint64(d)),
	))
}

func paragraphStyleBytes(r runSpec) []byte {
	parts := [][]byte{pbInt32Field(fieldStyleType, r.styleType)}
	if r.indent > 0 {
		parts = append(parts, pbVarintField(fieldIndentAmount, uint64(r.indent)))
	}
	if r.checkbox {
		parts = append(parts, checklist(r.checked))
	}
	return concat(parts...)
}

func runBytes(r runSpec) []byte {
	parts := [][]byte{pbVarintField(fieldRunLength, uint64(r.length))}
	if r.hasStyle {
		parts = append(parts, pbBytesField(fieldParagraphStyle, paragraphStyleBytes(r)))
	}
	if r.attachUTI != "" {
		info := concat(
			pbStringField(fieldAttachIdentifier, r.attachUUID),
			pbStringField(fieldAttachTypeUTI, r.attachUTI),
		)
		parts = append(parts, pbBytesField(fieldAttachmentInfo, info))
	}
	return concat(parts...)
}

func noteBodyBytes(text string, runs []runSpec) []byte {
	noteParts := [][]byte{pbStringField(fieldNoteText, text)}
	for _, r := range runs {
		noteParts = append(noteParts, pbBytesField(fieldAttributeRun, runBytes(r)))
	}
	note := concat(noteParts...)
	document := pbBytesField(fieldNote, note)
	store := pbBytesField(fieldDocument, document)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(store)
	_ = zw.Close()
	return buf.Bytes()
}

func decode(t *testing.T, text string, runs []runSpec) string {
	t.Helper()
	md, err := DecodeMarkdown(noteBodyBytes(text, runs), nil)
	if err != nil {
		t.Fatalf("DecodeMarkdown: %v", err)
	}
	return md
}

// --- tests ---

func TestHeadingsAndPlainParagraphs(t *testing.T) {
	// "Title\nHeading\nSub\nplain\n"
	got := decode(t, "Title\nHeading\nSub\nplain\n", []runSpec{
		{length: 6, styleType: styleTitle, hasStyle: true},
		{length: 8, styleType: styleHeading, hasStyle: true},
		{length: 4, styleType: styleSubheading, hasStyle: true},
		{length: 6, styleType: styleDefault, hasStyle: true},
	})
	want := "Title\n## Heading\n### Sub\nplain"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBulletedAndNumberedLists(t *testing.T) {
	got := decode(t, "a\nb\nc\n", []runSpec{
		{length: 2, styleType: styleDottedList, hasStyle: true},
		{length: 2, styleType: styleDashedList, hasStyle: true},
		{length: 2, styleType: styleNumbered, hasStyle: true},
	})
	want := "- a\n- b\n1. c"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNumberedListIncrementsAndResets(t *testing.T) {
	got := decode(t, "a\nb\nx\nc\n", []runSpec{
		{length: 2, styleType: styleNumbered, hasStyle: true},
		{length: 2, styleType: styleNumbered, hasStyle: true},
		{length: 2, styleType: styleDefault, hasStyle: true},
		{length: 2, styleType: styleNumbered, hasStyle: true},
	})
	want := "1. a\n2. b\nx\n1. c"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChecklistState(t *testing.T) {
	got := decode(t, "done\ntodo\n", []runSpec{
		{length: 5, styleType: styleCheckbox, hasStyle: true, checkbox: true, checked: true},
		{length: 5, styleType: styleCheckbox, hasStyle: true, checkbox: true, checked: false},
	})
	want := "- [x] done\n- [ ] todo"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestListIndentation(t *testing.T) {
	got := decode(t, "top\nnested\n", []runSpec{
		{length: 4, styleType: styleDottedList, hasStyle: true, indent: 0},
		{length: 7, styleType: styleDottedList, hasStyle: true, indent: 2},
	})
	want := "- top\n    - nested"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParagraphStyleCarriesForwardAcrossStylelessRuns(t *testing.T) {
	// A styleless second run continues the heading paragraph.
	got := decode(t, "Head", []runSpec{
		{length: 2, styleType: styleHeading, hasStyle: true},
		{length: 2}, // no paragraph_style — continues the heading
	})
	want := "## Head"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestUTF16AstralAlignment is the emoji edge case: an astral character is two
// UTF-16 code units but one Go rune. A run length counts UTF-16 units, so a
// naive rune/byte slice would misalign every run after the emoji. The second
// run must land exactly on "b".
func TestUTF16AstralAlignment(t *testing.T) {
	// text "😀a\nb\n": UTF-16 units 😀=2, a=1, \n=1, b=1, \n=1 = 6.
	got := decode(t, "😀a\nb\n", []runSpec{
		{length: 4, styleType: styleCheckbox, hasStyle: true, checkbox: true, checked: true}, // "😀a\n"
		{length: 2, styleType: styleDefault, hasStyle: true},                                 // "b\n"
	})
	want := "- [x] 😀a\nb"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAttachmentMarkers(t *testing.T) {
	cases := []struct {
		uti  string
		want string
	}{
		{"public.jpeg", "[image]"},
		{"public.png", "[image]"},
		{"com.apple.notes.gallery", "[attachment: gallery]"},
		{"com.apple.notes.inlinetextattachment.calculateresult", "[attachment: calculation]"},
		{"com.adobe.pdf", "[attachment: pdf]"},
		{"com.example.thing", "[attachment: thing]"}, // unknown → last dot segment
	}
	for _, tc := range cases {
		// text is a single object-replacement char followed by newline.
		got := decode(t, "￼\n", []runSpec{
			{length: 1, styleType: styleDefault, hasStyle: true, attachUTI: tc.uti, attachUUID: "uuid-1"},
			{length: 1, styleType: styleDefault, hasStyle: true},
		})
		if got != tc.want {
			t.Fatalf("uti %s: got %q, want %q", tc.uti, got, tc.want)
		}
	}
}

func TestTableWithoutResolverRendersNotCaptured(t *testing.T) {
	got := decode(t, "￼\n", []runSpec{
		{length: 1, styleType: styleDefault, hasStyle: true, attachUTI: utiTable, attachUUID: "table-1"},
		{length: 1, styleType: styleDefault, hasStyle: true},
	})
	if got != tableNotCaptured {
		t.Fatalf("got %q, want %q", got, tableNotCaptured)
	}
}

func TestTableResolverInvokedWithAttachmentUUID(t *testing.T) {
	var askedFor string
	resolve := func(uuid string) ([]byte, bool) {
		askedFor = uuid
		return nil, false // no bytes → still renders not-captured
	}
	md, err := DecodeMarkdown(noteBodyBytes("￼\n", []runSpec{
		{length: 1, styleType: styleDefault, hasStyle: true, attachUTI: utiTable, attachUUID: "table-xyz"},
		{length: 1, styleType: styleDefault, hasStyle: true},
	}), resolve)
	if err != nil {
		t.Fatal(err)
	}
	if askedFor != "table-xyz" {
		t.Fatalf("resolver asked for %q, want table-xyz", askedFor)
	}
	if md != tableNotCaptured {
		t.Fatalf("got %q, want not-captured marker", md)
	}
}

func TestTableAttachmentUUIDsFindsTables(t *testing.T) {
	uuids, err := TableAttachmentUUIDs(noteBodyBytes("￼￼\n", []runSpec{
		{length: 1, styleType: styleDefault, hasStyle: true, attachUTI: utiTable, attachUUID: "t-1"},
		{length: 1, styleType: styleDefault, hasStyle: true, attachUTI: "public.jpeg", attachUUID: "img-1"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(uuids) != 1 || uuids[0] != "t-1" {
		t.Fatalf("uuids = %v, want [t-1]", uuids)
	}
}

func TestRenderPipeTable(t *testing.T) {
	got := renderPipeTable([][]string{
		{"a", "b|c"},
		{"1", "2\n3"},
	})
	want := "| a | b\\|c |\n| --- | --- |\n| 1 | 2 3 |"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInflateRejectsLegacyBinaryPlist(t *testing.T) {
	_, err := Inflate([]byte("bplist00\x00\x01"))
	if err == nil {
		t.Fatal("expected error for bplist body")
	}
	want := "legacy binary-plist"
	if !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Fatalf("error %q does not mention %q", err.Error(), want)
	}
}

// TestPositiveControlDetectsCorruptBody is a positive control: it feeds a body
// that is NOT valid compressed protobuf and asserts the decoder reports an
// error. If the decode path were vacuously returning success (or empty text
// with no error), this test would fail — which is how we know the suite can
// actually catch a broken decoder, not just rubber-stamp green.
func TestPositiveControlDetectsCorruptBody(t *testing.T) {
	corrupt := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	if _, err := DecodeMarkdown(corrupt, nil); err == nil {
		t.Fatal("corrupt body decoded without error — decode path is not actually validating input")
	}
}
