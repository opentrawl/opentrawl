package messages

import "testing"

func TestNormalizePhoneMatchesClawdexShape(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"+1 (415) 734-7847", "14157347847"},
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
		{"+1 (415) 734-7847", true},
		{"service123", false},
		{"person@example.test", false},
		{"opaque", false},
	} {
		if got := LooksPhoneLike(tc.in); got != tc.want {
			t.Fatalf("LooksPhoneLike(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
