package telecrawl

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func TestSetupRequirementMappingIsExplicitlyEmpty(t *testing.T) {
	status, err := New().Status(context.Background(), &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "telegram.db")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.SetupRequirements) != 0 {
		t.Fatalf("telegram requirements = %#v", status.SetupRequirements)
	}
	forbidden := []control.SetupKind{control.SetupKindAccount, control.SetupKindPairing}
	for _, requirement := range status.SetupRequirements {
		for _, kind := range forbidden {
			if requirement.Kind == kind {
				t.Fatalf("telegram requirement = %#v", requirement)
			}
		}
	}
}

func TestSourceStoreCheckExplainsSelectedTelegramProduct(t *testing.T) {
	const remedy = "Open the selected Telegram app, then run trawl telegram sync. OpenTrawl reuses its existing local session."
	tests := []struct {
		name    string
		report  telegramdesktop.Report
		state   string
		message string
		remedy  string
	}{
		{
			name:    "native readable",
			report:  telegramdesktop.Report{Product: "telegram-macos", Exists: true, Accessible: true, Store: "telegram-macos-postbox"},
			state:   "ok",
			message: "Telegram for macOS is selected. Its local data is readable.",
		},
		{
			name:    "native store absent",
			report:  telegramdesktop.Report{Product: "telegram-macos", Exists: true, Store: "empty"},
			state:   "missing",
			message: "Telegram for macOS is selected, but its local data store was not found.",
			remedy:  remedy,
		},
		{
			name:    "native store unreadable",
			report:  telegramdesktop.Report{Product: "telegram-macos", Exists: true, Store: "unsupported-file", Error: "synthetic unreadable store"},
			state:   "missing",
			message: "Telegram for macOS is selected, but its local data store cannot be read.",
			remedy:  remedy,
		},
		{
			name:    "desktop fallback readable",
			report:  telegramdesktop.Report{Product: "telegram-desktop", Exists: true, Accessible: true, Store: "tdesktop-binary"},
			state:   "ok",
			message: "Telegram Desktop is selected because Telegram for macOS is not installed. Its local data is readable.",
		},
		{
			name:    "desktop fallback absent",
			report:  telegramdesktop.Report{Product: "telegram-desktop", Store: "missing"},
			state:   "missing",
			message: "Telegram Desktop is selected because Telegram for macOS is not installed, but its local data store was not found.",
			remedy:  remedy,
		},
		{
			name:    "desktop fallback unreadable",
			report:  telegramdesktop.Report{Product: "telegram-desktop", Exists: true, Store: "unsupported-file", Error: "synthetic unreadable store"},
			state:   "missing",
			message: "Telegram Desktop is selected because Telegram for macOS is not installed, but its local data store cannot be read.",
			remedy:  remedy,
		},
		{
			name:    "explicit desktop path",
			report:  telegramdesktop.Report{Product: "telegram-desktop", Explicit: true, Exists: true, Accessible: true, Store: "tdesktop-binary"},
			state:   "ok",
			message: "Telegram Desktop is selected from --path. Its local data is readable.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check := sourceStoreCheck(test.report)
			if check.State != test.state || check.Message != test.message || check.Remedy != test.remedy {
				t.Fatalf("doctor input=%+v output=%+v", test.report, check)
			}
			t.Logf("doctor input=%+v", test.report)
			t.Logf("doctor output state=%q message=%q remedy=%q", check.State, check.Message, check.Remedy)
		})
	}
}
