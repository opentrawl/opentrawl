package cli

import (
	"testing"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

func TestAppWireRecognisesOnlyThePrivateHelperCommand(t *testing.T) {
	if !isAppWireCommand([]string{"__app", "status"}) || isAppWireCommand([]string{"status"}) {
		t.Fatal("helper command recognition changed")
	}
}

func TestAppPhotosRequestFailureKeepsRefreshedSources(t *testing.T) {
	response := appPhotosRequestFailure(&federationv1.StatusResponse{
		Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		Sources: []*federationv1.SourceStatus{{
			Manifest: &federationv1.SourceManifest{SourceId: "photos", Surface: "Photos"},
		}},
	}, Source{ID: "photos", DisplayName: "Photos"})
	if response.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.Sources) != 1 || len(response.Failures) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Failures[0].SourceId != "photos" || response.Failures[0].Message != "Photos access could not be requested." {
		t.Fatalf("failure = %#v", response.Failures[0])
	}
}
