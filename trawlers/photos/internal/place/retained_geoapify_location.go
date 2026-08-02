package place

import (
	"errors"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReprojectRetainedGeoapifyReverseGeocodingEvidence verifies retained proof
// evidence with the live Geoapify parser. This entry point exists only on the
// private one-off import branch.
func ReprojectRetainedGeoapifyReverseGeocodingEvidence(
	request *locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest,
	retainedExchange *locationwire.ProviderExchange,
	legacyOperationCompletedAt *timestamppb.Timestamp,
) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.Input) != nil {
		return nil, errors.New("retained Geoapify reverse-geocoding request is incomplete")
	}
	exchange, err := retainedResponseExchange(retainedExchange)
	if err != nil {
		return nil, err
	}
	outcome := &locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome{Request: request, Exchange: exchange}
	address, err := parseGeoapifyAddress(exchange.ExactResponse)
	if err != nil {
		return nil, err
	}
	if address == nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else {
		outcome.Address = address
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt, err = retainedCompletionTime(legacyOperationCompletedAt)
	return outcome, err
}

// ReprojectRetainedGeoapifyNearbyPlaceEvidence verifies retained proof
// evidence with the live Geoapify parser. This entry point exists only on the
// private one-off import branch.
func ReprojectRetainedGeoapifyNearbyPlaceEvidence(
	request *locationwire.AcquireGeoapifyNearbyPlaceEvidenceRequest,
	retainedExchange *locationwire.ProviderExchange,
	legacyOperationCompletedAt *timestamppb.Timestamp,
) (*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.Input) != nil {
		return nil, errors.New("retained Geoapify nearby-place request is incomplete")
	}
	if request.RadiusMeters <= 0 || request.MaximumCandidates <= 0 || request.MaximumCandidates > MaximumNearbyPlaceCandidates {
		return nil, errors.New("retained Geoapify nearby-place request bound is invalid")
	}
	if !captureLocationInputsMatch(request.Input, request.GetKnownPlaceOutcome().GetRequest().GetInput()) {
		return nil, errors.New("retained Geoapify nearby-place known-place input differs from its capture input")
	}
	outcome := &locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome{Request: request}
	var err error
	if len(request.GetKnownPlaceOutcome().GetMatches()) > 0 {
		if retainedExchange == nil || retainedExchange.GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE || retainedExchange.GetTransmissionStarted() || len(retainedExchange.GetExactResponse()) != 0 || retainedExchange.GetFailure() != nil {
			return nil, errors.New("retained Geoapify nearby-place proof is not an honest known-place skip")
		}
		outcome.Exchange = proto.Clone(retainedExchange).(*locationwire.ProviderExchange)
	} else {
		outcome.Exchange, err = retainedResponseExchange(retainedExchange)
		if err != nil {
			return nil, err
		}
		outcome.Candidates, err = parseGeoapifyCandidates(outcome.Exchange.ExactResponse, request.MaximumCandidates)
		if err != nil {
			return nil, err
		}
		if len(outcome.Candidates) == 0 {
			outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
		} else {
			outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
		}
	}
	outcome.CompletedAt, err = retainedCompletionTime(legacyOperationCompletedAt)
	return outcome, err
}

func retainedResponseExchange(retainedExchange *locationwire.ProviderExchange) (*locationwire.ProviderExchange, error) {
	if retainedExchange == nil || !retainedExchange.GetTransmissionStarted() || len(retainedExchange.GetExactResponse()) == 0 || retainedExchange.GetFailure() != nil {
		return nil, errors.New("retained provider exchange has no successful response evidence")
	}
	if len(retainedExchange.GetExactResponse()) > maxRawEvidenceBytes {
		return nil, errors.New("retained provider response exceeds the evidence limit")
	}
	exchange := proto.Clone(retainedExchange).(*locationwire.ProviderExchange)
	exchange.State = locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED
	return exchange, nil
}

func retainedCompletionTime(retained *timestamppb.Timestamp) (*timestamppb.Timestamp, error) {
	if retained == nil {
		return nil, errors.New("retained provider completion time is missing")
	}
	if err := retained.CheckValid(); err != nil {
		return nil, errors.New("retained provider completion time is invalid")
	}
	return retained, nil
}
