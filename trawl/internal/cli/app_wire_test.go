package cli

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func TestAppWireRecognisesOnlyThePrivateHelperCommand(t *testing.T) {
	if !isAppWireCommand([]string{"__app", "status"}) || isAppWireCommand([]string{"status"}) {
		t.Fatal("helper command recognition changed")
	}
}

func TestAppResourceReturnsOneBoundedOpaqueFrame(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "photoscrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","open"],"id":"photos","display_name":"Photos"}`,
	})
	t.Setenv("PATH", binDir)
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	store, err := ckstore.Open(context.Background(), ckstore.Options{Path: filepath.Join(home, ".opentrawl", "photos", "photos.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "__app", "resource", "photos", "photos:resource/example-1", "32")
	if code != 0 || stderr != "" {
		t.Fatalf("resource code=%d stderr=%q", code, stderr)
	}
	frame := []byte(stdout)
	if len(frame) < 4 || int(binary.LittleEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatalf("resource frame length = %d", len(frame))
	}
	var response presentationv1.ResourceResponse
	if err := proto.Unmarshal(frame[4:], &response); err != nil {
		t.Fatal(err)
	}
	if response.GetResourceRef() != "photos:resource/example-1" || response.GetContentType() != "image/jpeg" || string(response.GetData()) != "synthetic resource bytes" {
		t.Fatalf("resource response = %#v", &response)
	}
}

func TestAppPhotosRequestFailureKeepsRefreshedSources(t *testing.T) {
	response := appPhotosRequestFailure(&federationv1.StatusResponse{
		Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		Sources: []*federationv1.SourceStatus{{
			Manifest: &federationv1.SourceManifest{SourceId: "photos", DisplayName: "Photos"},
		}},
	}, Source{ID: "photos", DisplayName: "Photos"})
	if response.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.Sources) != 1 || len(response.Failures) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Failures[0].SourceId != "photos" || response.Failures[0].Message != "Photos access could not be requested." {
		t.Fatalf("failure = %#v", response.Failures[0])
	}
}
