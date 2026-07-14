package federation

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

func TestOpenPreservesRequestedRefAndValidatesReturnedRecord(t *testing.T) {
	called := 0
	response := Open(context.Background(), []OpenSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(_ context.Context, ref, anchorID string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
			called++
			if ref != "notes:note/example-1" || anchorID != "" {
				t.Fatalf("callback ref = %q", ref)
			}
			return validOpenRecord("notes", "notes:note/example-1"), nil
		},
	}}, "notes", "  notes:note/example-1  ", "")
	if called != 1 || response.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || response.GetRequestedRef() != "  notes:note/example-1  " || response.GetRecord().GetOpenRef() != "notes:note/example-1" || response.GetFailure() != nil {
		t.Fatalf("response = %#v, called=%d", response, called)
	}
}

func TestOpenAcceptsUnqualifiedShortRefAndRejectsInvalidRefsBeforeCallback(t *testing.T) {
	called := 0
	source := OpenSource{Manifest: manifestFixture("notes", "Notes"), Run: func(_ context.Context, ref, _ string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
		called++
		if ref != "short-7" {
			t.Fatalf("short ref = %q", ref)
		}
		return validOpenRecord("notes", "notes:note/example-1"), nil
	}}
	if response := Open(context.Background(), []OpenSource{source}, "notes", " short-7 ", ""); response.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || called != 1 {
		t.Fatalf("short ref response = %#v, called=%d", response, called)
	}
	for _, ref := range []string{"", "   ", "gmail:message/1", "other:note/1", "notes:", ":note/1"} {
		response := Open(context.Background(), []OpenSource{source}, "notes", ref, "")
		if response.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailure().GetCode() != federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT || called != 1 {
			t.Fatalf("ref %q response = %#v, called=%d", ref, response, called)
		}
	}
}

func TestOpenFailureShapesAndCallbackRules(t *testing.T) {
	manifest := manifestFixture("notes", "Notes")
	called := false
	undeclared := Open(context.Background(), []OpenSource{{Manifest: manifest, SkipReason: "Open is not supported.", Run: func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
		called = true
		return nil, nil
	}}}, "notes", "short-7", "")
	if called || undeclared.GetFailure().GetCode() != federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE || undeclared.GetFailure().GetMessage() != "Open is not supported." {
		t.Fatalf("undeclared = %#v, called=%t", undeclared, called)
	}
	unknown := Open(context.Background(), []OpenSource{{Manifest: manifest}}, "gmail", "short-7", "")
	if unknown.GetFailure().GetCode() != federationv1.FailureCode_FAILURE_CODE_NOT_FOUND || unknown.GetRecord() != nil {
		t.Fatalf("unknown = %#v", unknown)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledCalls := 0
	response := Open(cancelled, []OpenSource{{Manifest: manifest, Run: func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
		cancelledCalls++
		return validOpenRecord("notes", "notes:one"), nil
	}}}, "notes", "short-7", "")
	if cancelledCalls != 0 || response.GetFailure().GetCode() != federationv1.FailureCode_FAILURE_CODE_CANCELLED {
		t.Fatalf("cancelled response = %#v, calls=%d", response, cancelledCalls)
	}
	cases := []struct {
		name string
		run  func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure)
		want federationv1.FailureCode
	}{
		{"both returns failure", func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
			return validOpenRecord("notes", "notes:one"), &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE, Message: "unavailable"}
		}, federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE},
		{"nil record", func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) { return nil, nil }, federationv1.FailureCode_FAILURE_CODE_INTERNAL},
		{"panic", func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) { panic("synthetic") }, federationv1.FailureCode_FAILURE_CODE_INTERNAL},
		{"invalid record", func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
			return &openv1.OpenRecord{SourceId: "notes"}, nil
		}, federationv1.FailureCode_FAILURE_CODE_INTERNAL},
		{"foreign record", func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
			return validOpenRecord("gmail", "gmail:one"), nil
		}, federationv1.FailureCode_FAILURE_CODE_INTERNAL},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := Open(context.Background(), []OpenSource{{Manifest: manifest, Run: test.run}}, "notes", "short-7", "")
			if response.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetRecord() != nil || response.GetFailure().GetCode() != test.want {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestFailureForErrorPreservesTypedErrorAndPinsCodes(t *testing.T) {
	manifest := manifestFixture("notes", "Notes")
	typed := typedFailure{body: ckoutput.ErrorBody{Code: "authentication_required", Message: " Sign in again. ", Remedy: " run trawl notes login "}}
	if got := FailureForError(manifest, "open", typed); got.GetCode() != federationv1.FailureCode_FAILURE_CODE_AUTHENTICATION || got.GetMessage() != " Sign in again. " || got.GetRemedy() != " run trawl notes login " {
		t.Fatalf("typed failure = %#v", got)
	}
	for _, test := range []struct {
		err  error
		want federationv1.FailureCode
	}{
		{context.Canceled, federationv1.FailureCode_FAILURE_CODE_CANCELLED},
		{context.DeadlineExceeded, federationv1.FailureCode_FAILURE_CODE_TIMEOUT},
		{typedFailure{body: ckoutput.ErrorBody{Code: "timeout"}}, federationv1.FailureCode_FAILURE_CODE_TIMEOUT},
		{typedFailure{body: ckoutput.ErrorBody{Code: "permission_denied"}}, federationv1.FailureCode_FAILURE_CODE_PERMISSION},
		{typedFailure{body: ckoutput.ErrorBody{Code: "authentication"}}, federationv1.FailureCode_FAILURE_CODE_AUTHENTICATION},
		{typedFailure{body: ckoutput.ErrorBody{Code: "ambiguous_short_ref"}}, federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT},
		{typedFailure{body: ckoutput.ErrorBody{Code: "unknown_short_ref"}}, federationv1.FailureCode_FAILURE_CODE_NOT_FOUND},
		{typedFailure{body: ckoutput.ErrorBody{Code: "unavailable"}}, federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE},
		{typedFailure{body: ckoutput.ErrorBody{Code: "command_failed"}}, federationv1.FailureCode_FAILURE_CODE_INTERNAL},
		{errors.ErrUnsupported, federationv1.FailureCode_FAILURE_CODE_INTERNAL},
	} {
		if got := FailureForError(manifest, "open", test.err); got.GetCode() != test.want || got.GetRemedy() != "trawl doctor notes" {
			t.Fatalf("FailureForError(%v) = %#v", test.err, got)
		}
	}
}

type typedFailure struct{ body ckoutput.ErrorBody }

func (e typedFailure) Error() string                 { return e.body.Message }
func (e typedFailure) ErrorBody() ckoutput.ErrorBody { return e.body }

func validOpenRecord(sourceID, ref string) *openv1.OpenRecord {
	return &openv1.OpenRecord{
		SourceId: sourceID,
		OpenRef:  ref,
		Data:     &anypb.Any{TypeUrl: "type.example/Open"},
		Presentation: &presentationv1.PresentationDocument{
			Title:           "Synthetic record",
			PrimaryAnchorId: "match",
			Blocks: []*presentationv1.Block{{
				AnchorId: "match",
				Content:  &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: "Synthetic matching passage."}},
			}},
		},
	}
}

var _ control.Manifest
