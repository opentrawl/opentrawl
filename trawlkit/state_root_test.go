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

func TestConfiguredStateRootOwnsEveryDerivedSourcePath(t *testing.T) {
	home := t.TempDir()
	isolate := filepath.Join(t.TempDir(), "alpha-state")
	t.Setenv("HOME", home)
	t.Setenv(StateRootEnvironment, isolate)

	paths, err := resolveSourcePaths("", Info{ID: "messages"})
	if err != nil {
		t.Fatal(err)
	}
	if paths.StateRoot != isolate || paths.Archive != filepath.Join(isolate, "messages", "messages.db") || paths.Config != filepath.Join(isolate, "messages", "config.toml") || paths.Logs != filepath.Join(isolate, "messages", "logs") {
		t.Fatalf("isolated paths = %#v", paths)
	}
	if _, err := os.Stat(filepath.Join(home, ".opentrawl")); !os.IsNotExist(err) {
		t.Fatalf("production state root was touched: %v", err)
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
