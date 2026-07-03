package main

import (
	"context"
	"strings"
	"testing"
)

func TestUsageMentionsLabVerbs(t *testing.T) {
	err := run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: photoscrawl-lab <place-context|eval-card>") {
		t.Fatalf("unexpected usage error: %v", err)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("gemma4:31b, gemini-flash-latest,")
	want := []string{"gemma4:31b", "gemini-flash-latest"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitList = %#v", got)
		}
	}
}
