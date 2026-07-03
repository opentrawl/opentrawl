package model

import "testing"

func TestSlugStable(t *testing.T) {
	if got := Slug("Sally O'Malley"); got != "sally-o-malley" {
		t.Fatalf("Slug = %q", got)
	}
	if got := NormalizePhone("+1 (555) 010-7847"); got != "15550107847" {
		t.Fatalf("NormalizePhone = %q", got)
	}
	if got := NormalizePhone("0043 664 104 2436"); got != "436641042436" {
		t.Fatalf("NormalizePhone 00 = %q", got)
	}
	if got := NormalizeEmail(" ADA@Example.COM "); got != "ada@example.com" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
	if got := NormalizeName(" Ada   Lovelace "); got != "ada lovelace" {
		t.Fatalf("NormalizeName = %q", got)
	}
	if got := PathSlug("/tmp/ada/person.md"); got != "ada" {
		t.Fatalf("PathSlug = %q", got)
	}
	if got := Slug("***"); got != "person" {
		t.Fatalf("empty Slug = %q", got)
	}
}

func TestNormalizePhoneStripsCountryCodeTrunkZero(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phone string
		want  string
	}{
		{name: "dutch plus", phone: "+31 0600 000 000", want: "31600000000"},
		{name: "dutch 00", phone: "0031 0600 000 000", want: "31600000000"},
		{name: "dutch stored digits", phone: "310600000000", want: "31600000000"},
		{name: "dutch canonical", phone: "+31 600 000 000", want: "31600000000"},
		{name: "uk plus", phone: "+44 07123 456789", want: "447123456789"},
		{name: "austrian 00", phone: "0043 0664 104 2436", want: "436641042436"},
		{name: "plain national", phone: "0600000000", want: "0600000000"},
		{name: "short after country code", phone: "+31 0123", want: "310123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizePhone(tc.phone)
			if got != tc.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", tc.phone, got, tc.want)
			}
			if again := NormalizePhone(got); again != got {
				t.Fatalf("NormalizePhone is not idempotent: %q became %q", got, again)
			}
		})
	}
}
