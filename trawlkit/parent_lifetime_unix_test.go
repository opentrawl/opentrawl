//go:build !windows

package trawlkit

import (
	"context"
	"testing"
)

func TestRunWireChildRequiresParentSupervisionBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		value       string
		unset       bool
		wantMessage string
	}{
		{name: "missing", unset: true, wantMessage: "TRAWLKIT_PARENT_FD is required"},
		{name: "empty", wantMessage: "TRAWLKIT_PARENT_FD is required"},
		{name: "malformed", value: "not-a-fd", wantMessage: "TRAWLKIT_PARENT_FD is invalid"},
		{name: "closed", value: "999999", wantMessage: "TRAWLKIT_PARENT_FD is invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnvForTest(t, childStateRootEnv, t.TempDir())
			setEnvForTest(t, childRunIDEnv, "run-1")
			if tc.unset {
				unsetEnvForTest(t, childParentFDEnv)
			} else {
				setEnvForTest(t, childParentFDEnv, tc.value)
			}

			mutated := false
			source := &testCrawler{syncFn: func(context.Context, *Request) (*SyncReport, error) {
				mutated = true
				return &SyncReport{Added: 1}, nil
			}}
			code, frame, stderr := runWireInvocationForTest(t, []string{HiddenWireSubcommand, "--json", "testcrawl", "sync"}, source, runOptions{})
			if code != 2 || stderr != "" || frame.errorBody == nil {
				t.Fatalf("wire parent supervision failure code=%d frame=%#v stderr=%q", code, frame, stderr)
			}
			if mutated {
				t.Fatal("hidden wire reached mutation without parent supervision")
			}
			if frame.errorBody.Code != "usage" || frame.errorBody.Message != tc.wantMessage || frame.errorBody.Remedy != childWireEnvRemedy {
				t.Fatalf("wire parent supervision error = %#v", frame.errorBody)
			}
		})
	}
}

func TestRunWireChildWithParentSupervisionCanMutate(t *testing.T) {
	setEnvForTest(t, childStateRootEnv, t.TempDir())
	setEnvForTest(t, childRunIDEnv, "run-1")
	mutated := false
	source := &testCrawler{syncFn: func(context.Context, *Request) (*SyncReport, error) {
		mutated = true
		return &SyncReport{Added: 1}, nil
	}}

	code, frame, stderr := runWireForTest(t, []string{HiddenWireSubcommand, "--json", "testcrawl", "sync"}, source, runOptions{})
	if code != 0 || stderr != "" || frame.errorBody != nil {
		t.Fatalf("supervised wire code=%d frame=%#v stderr=%q", code, frame, stderr)
	}
	if !mutated {
		t.Fatal("supervised hidden wire did not reach mutation")
	}
}
