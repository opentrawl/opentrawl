package messages

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestNormalizePhoneMatchesClawdexShape(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"+1 (555) 010-7847", "15550107847"},
		{"0043 664 104 2436", "436641042436"},
		{"opaque", ""},
	} {
		if got := NormalizePhone(tc.in); got != tc.want {
			t.Fatalf("NormalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLooksPhoneLikeAllowsShortCodesButRejectsOpaqueIDs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"42777", true},
		{"+1 (555) 010-7847", true},
		{"service123", false},
		{"person@example.test", false},
		{"opaque", false},
	} {
		if got := LooksPhoneLike(tc.in); got != tc.want {
			t.Fatalf("LooksPhoneLike(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDecodeAttributedBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "short", text: "Hello world"},
		{name: "long", text: strings.Repeat("This text is longer than one byte can count. ", 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeAttributedBody(makeStreamtypedAttributedBody(tc.text))
			if got != tc.text {
				t.Fatalf("got %q, want %q", got, tc.text)
			}
		})
	}
}

func TestDecodeStreamtypedLength(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want int
		pos  int
	}{
		{name: "inline", body: []byte{0x7f}, want: 0x7f, pos: 1},
		{name: "int16", body: []byte{0x81, 0x34, 0x12}, want: 0x1234, pos: 3},
		{name: "int32", body: []byte{0x82, 0x78, 0x56, 0x34, 0x12}, want: 0x12345678, pos: 5},
		{name: "int64", body: []byte{0x83, 0x34, 0x12, 0, 0, 0, 0, 0, 0}, want: 0x1234, pos: 9},
		{name: "truncated", body: []byte{0x83, 1, 2}, want: 0, pos: 1},
		{name: "overflow", body: []byte{0x83, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, want: 0, pos: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, pos := decodeStreamtypedLength(tc.body, 0)
			if got != tc.want || pos != tc.pos {
				t.Fatalf("got (%d, %d), want (%d, %d)", got, pos, tc.want, tc.pos)
			}
		})
	}
}

func TestDecodeAttributedBodyRejectsTruncatedText(t *testing.T) {
	body := makeStreamtypedAttributedBody("short")
	marker := bytes.Index(body, []byte("\x84\x01+"))
	body[marker+3] = 0x7f
	if got := decodeAttributedBody(body); got != "" {
		t.Fatalf("decoded truncated body as %q", got)
	}
}

func makeStreamtypedAttributedBody(text string) []byte {
	var out []byte
	out = append(out, "\x04\x0bstreamtyped\x81\xe8\x03\x84\x01@\x84\x84\x84"...)
	out = append(out, "\x12NSAttributedString"...)
	out = append(out, "\x00\x84\x84\x08NSObject\x00\x85\x92\x84\x84\x84\x08NSString\x01\x94"...)
	out = append(out, "\x84\x01+"...)
	if len(text) < 0x80 {
		out = append(out, byte(len(text)))
	} else {
		out = append(out, 0x81)
		out = binary.LittleEndian.AppendUint16(out, uint16(len(text)))
	}
	out = append(out, text...)
	out = append(out, 0x86)
	return out
}
