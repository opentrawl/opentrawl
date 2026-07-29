package trawlkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRootDefaultsToOpenTrawlUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	previous, existed := os.LookupEnv(StateRootEnvironment)
	_ = os.Unsetenv(StateRootEnvironment)
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(StateRootEnvironment, previous)
		} else {
			_ = os.Unsetenv(StateRootEnvironment)
		}
	})

	root, err := ResolveStateRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".opentrawl"); root != want {
		t.Fatalf("state root = %q, want %q", root, want)
	}
}

func TestConfiguredStateRootCannotFallBackToHome(t *testing.T) {
	for _, value := range []string{"", "relative/state"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(StateRootEnvironment, value)
			if _, err := ResolveStateRoot(""); err == nil {
				t.Fatalf("ResolveStateRoot accepted %q", value)
			}
		})
	}
}
