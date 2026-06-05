package messages

import "testing"

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
		{name: "long", text: "This is a longer message that exercises the multi-byte length path in the streamtyped attributedBody decoder."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeAttributedBody(makeStreamtypedAttributedBody(tc.text))
			if got != tc.text {
				t.Fatalf("got %q, want %q", got, tc.text)
			}
		})
	}
}

func makeStreamtypedAttributedBody(text string) []byte {
	var out []byte
	out = append(out, "\x04\x0bstreamtyped\x81\xe8\x03\x84\x01@\x84\x84\x84"...)
	out = append(out, "\x12NSAttributedString"...)
	out = append(out, "\x00\x84\x84\x08NSObject\x00\x85\x92\x84\x84\x84\x08NSString\x01\x94"...)
	out = append(out, "\x84\x01+"...)
	out = append(out, 0x81, byte(len(text)), 0x92, 0x00)
	out = append(out, text...)
	out = append(out, 0x86)
	return out
}
