//go:build darwin

package place

import (
	"errors"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReprojectRetainedAppleReverseGeocodingEvidence parses a retained successful
// Apple adapter response through the live Apple reverse-geocoding parser. This
// entry point exists only on the private one-off import branch.
func ReprojectRetainedAppleReverseGeocodingEvidence(
	request *locationwire.AcquireAppleReverseGeocodingEvidenceRequest,
	exactRetainedAdapterResponse []byte,
	legacyOperationCompletedAt *timestamppb.Timestamp,
) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.Input) != nil {
		return nil, errors.New("retained Apple reverse-geocoding request is incomplete")
	}
	if len(exactRetainedAdapterResponse) == 0 {
		return nil, errors.New("retained Apple reverse-geocoding response is empty")
	}
	if len(exactRetainedAdapterResponse) > maxRawEvidenceBytes {
		return nil, errors.New("retained Apple reverse-geocoding response exceeds the evidence limit")
	}
	outcome := &locationwire.AcquireAppleReverseGeocodingEvidenceOutcome{
		Request: request,
		Exchange: &locationwire.ProviderExchange{
			State:               locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED,
			TransmissionStarted: true,
			ExactResponse:       append([]byte(nil), exactRetainedAdapterResponse...),
		},
	}
	outcome, err := ResumeAppleReverseGeocodingEvidence(outcome, nil)
	if err != nil {
		return nil, err
	}
	if legacyOperationCompletedAt != nil {
		if err := legacyOperationCompletedAt.CheckValid(); err != nil {
			return nil, errors.New("retained Apple reverse-geocoding completion time is invalid")
		}
		outcome.CompletedAt = legacyOperationCompletedAt
	}
	if !ProviderExchangeSatisfiesCurrentLocationEvidence(outcome.GetExchange(), false) || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SUCCEEDED || outcome.GetAddress() == nil {
		return nil, errors.New("retained Apple reverse-geocoding response does not produce current typed address evidence")
	}
	return outcome, nil
}
