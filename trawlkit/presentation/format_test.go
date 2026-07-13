package presentation

import "testing"

func TestTimestamp(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"utc", "2026-07-10T14:00:00Z", "10 July 2026 at 14:00"},
		{"offset", "2026-07-10T14:00:00+02:00", "10 July 2026 at 14:00"},
		{"fractional", "2026-07-10T14:00:00.125+02:00", "10 July 2026 at 14:00"},
		{"absent", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Timestamp(test.input)
			if err != nil || got != test.want {
				t.Fatalf("Timestamp(%q) = %q, %v; want %q, nil", test.input, got, err, test.want)
			}
		})
	}
	if _, err := Timestamp("not a timestamp"); err == nil {
		t.Fatal("Timestamp accepted an invalid value")
	}
}

func TestBytes(t *testing.T) {
	for _, test := range []struct {
		input int64
		want  string
	}{
		{0, "0 bytes"}, {1023, "1023 bytes"}, {1024, "1.0 KiB"}, {1536, "1.5 KiB"}, {1048576, "1.0 MiB"},
	} {
		if got := Bytes(test.input); got != test.want {
			t.Errorf("Bytes(%d) = %q, want %q", test.input, got, test.want)
		}
	}
}
