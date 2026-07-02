package main

import "testing"

func TestJoinedQueryPreservesLauncherArguments(t *testing.T) {
	if got := joinedQuery("hello", []string{"world", "photos"}); got != "hello world photos" {
		t.Fatalf("joined query = %q", got)
	}
	if got := joinedQuery("", []string{"hello", "world"}); got != "hello world" {
		t.Fatalf("positional query = %q", got)
	}
}

func TestStripTrailingJSON(t *testing.T) {
	args, ok := stripTrailingJSON([]string{"boat", "trip", "--json"})
	if !ok {
		t.Fatal("expected trailing JSON flag")
	}
	if got := joinedQuery("", args); got != "boat trip" {
		t.Fatalf("query after strip = %q", got)
	}

	args, ok = stripTrailingJSON([]string{"--json", "boat"})
	if ok {
		t.Fatal("did not expect non-trailing JSON flag to be stripped")
	}
	if got := joinedQuery("", args); got != "--json boat" {
		t.Fatalf("query without strip = %q", got)
	}
}
