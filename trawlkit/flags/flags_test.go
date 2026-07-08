package flags

import (
	"errors"
	"testing"
)

func TestLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		n        int
		limitSet bool
		want     int
		wantErr  error
	}{
		{name: "honored as given", n: 50, limitSet: true, want: 50},
		{name: "one is fine", n: 1, limitSet: true, want: 1},
		{name: "default is honored", n: 20, want: 20},
		{name: "zero is a usage error", n: 0, limitSet: true, wantErr: ErrLimitBelowOne},
		{name: "negative is a usage error", n: -3, limitSet: true, wantErr: ErrLimitBelowOne},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Limit(tc.n, tc.limitSet)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Limit(%d, %t) err = %v, want %v", tc.n, tc.limitSet, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("Limit(%d, %t) = %d, want %d", tc.n, tc.limitSet, got, tc.want)
			}
		})
	}
}
