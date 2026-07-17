package telecrawl

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestTelegramHistoryStateRoundTripIsCanonical(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "telegram.db")
	want := telegramHistoryState{
		CompletedDialogs: []string{"account:2", "account:1", "account:2", ""},
		DialogOffsets:    map[string]int{"account:3": 42, "invalid": 0},
	}
	if err := saveTelegramHistoryState(archive, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadTelegramHistoryState(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.CompletedDialogs, []string{"account:1", "account:2"}) || got.Complete {
		t.Fatalf("state = %#v", got)
	}
	if !reflect.DeepEqual(got.DialogOffsets, map[string]int{"account:3": 42}) {
		t.Fatalf("offsets = %#v", got.DialogOffsets)
	}
}

func TestMissingTelegramHistoryStateStartsIncomplete(t *testing.T) {
	t.Parallel()
	got, err := loadTelegramHistoryState(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Complete || len(got.CompletedDialogs) != 0 {
		t.Fatalf("state = %#v", got)
	}
}
