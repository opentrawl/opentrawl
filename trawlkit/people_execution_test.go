package trawlkit

import (
	"context"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/control"
)

type testPeopleReconciler struct{ *testCrawler }

func (c *testPeopleReconciler) ReconcilePeopleSnapshot(context.Context, *Request, string, *control.PeopleSnapshot) (*SyncReport, error) {
	return &SyncReport{}, nil
}

func TestPeopleReconciliationCannotBeCalledAsACommand(t *testing.T) {
	destination := &testPeopleReconciler{testCrawler: &testCrawler{}}
	code, _, stderr := runForTest([]string{internalPeopleReconcileVerb}, destination, defaultRunOptions())
	if code != 2 || !strings.Contains(stderr, "unknown verb") {
		t.Fatalf("direct People reconciliation code=%d stderr=%q", code, stderr)
	}
}
