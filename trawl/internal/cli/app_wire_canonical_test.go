package cli

import (
	"bytes"
	"testing"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"google.golang.org/protobuf/proto"
)

func TestAppWireFramesCanonicalResponses(t *testing.T) {
	message := &federationv1.SearchResponse{Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE, Order: federationv1.SearchOrder_SEARCH_ORDER_RECENCY, ResultLimit: appSearchLimit}
	var output bytes.Buffer
	if err := writeAppResponse(&output, message); err != nil {
		t.Fatal(err)
	}
	payload := output.Bytes()[4:]
	var decoded federationv1.SearchResponse
	if err := proto.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(message, &decoded) {
		t.Fatalf("decoded response = %v", &decoded)
	}
}
