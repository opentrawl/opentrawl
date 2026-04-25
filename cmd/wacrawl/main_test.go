package main

import "testing"

func TestRunHelp(t *testing.T) {
	if code := run([]string{"help"}); code != 0 {
		t.Fatalf("run help exit code = %d, want 0", code)
	}
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("run version exit code = %d, want 0", code)
	}
	if code := run([]string{"nope"}); code != 2 {
		t.Fatalf("run invalid exit code = %d, want 2", code)
	}
}
