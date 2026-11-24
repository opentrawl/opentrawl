package placelookup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	photonRequestTimeout = 30 * time.Second
	// Komoot asks for fair use of its public service. A self-hosted Photon
	// answers as fast as it can.
	publicPhotonMinimumTimeBetweenRequests = time.Second
	photonServerErrorRetryDelay            = time.Hour
	photonUserAgent                        = "OpenTrawl Photos (https://github.com/opentrawl/opentrawl)"
)

// LookUpPlacesNearSpotStep sends one request to Photon and returns what it
// answered. A failure that belongs to the service rather than the spot (no
// connection, or rate limiting) stops the run instead of failing every spot.
type LookUpPlacesNearSpotStep struct {
	photonBaseURL                    string
	minimumTimeBetweenPhotonRequests time.Duration
	photonHTTPClient                 *http.Client
	lastPhotonRequestTime            time.Time
}

func NewLookUpPlacesNearSpotStep(photonBaseURL string) *LookUpPlacesNearSpotStep {
	photonBaseURL = strings.TrimSuffix(photonBaseURL, "/")
	step := &LookUpPlacesNearSpotStep{
		photonBaseURL:    photonBaseURL,
		photonHTTPClient: &http.Client{Timeout: photonRequestTimeout},
	}
	if photonBaseURL == PublicPhotonBaseURL {
		step.minimumTimeBetweenPhotonRequests = publicPhotonMinimumTimeBetweenRequests
	}
	return step
}

func (*LookUpPlacesNearSpotStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_LOOK_UP_PLACES_NEAR_SPOT
}

func (step *LookUpPlacesNearSpotStep) RunPhotosStepOnOneInput(
	ctx context.Context,
	lookUpPlacesNearSpotRequest *photosstep.LookUpPlacesNearSpotRequest,
) (*photo.NearbyPlacesLookupResult, error) {
	requestSHA256, err := PlaceLookupRequestSHA256(lookUpPlacesNearSpotRequest)
	if err != nil {
		return nil, err
	}
	lookupResult := &photo.NearbyPlacesLookupResult{
		PlaceLookupRequestSha256: requestSHA256,
		PlaceProvider:            lookUpPlacesNearSpotRequest.GetPlaceProvider(),
		CameraSpotKey:            lookUpPlacesNearSpotRequest.GetCameraSpotKey(),
		PlaceLookupRequestText:   lookUpPlacesNearSpotRequest.GetPlaceLookupRequestText(),
	}
	photonRequestURL := step.photonBaseURL + lookUpPlacesNearSpotRequest.GetPlaceLookupRequestText()
	photonResponseStatusCode, photonResponseBody, err := step.sendPacedPhotonRequest(ctx, photonRequestURL)
	if err != nil {
		var failedPhotonRequest *url.Error
		if errors.As(err, &failedPhotonRequest) {
			err = failedPhotonRequest.Err
		}
		return nil, render.PhotosPlaceLookupServiceDidNotAnswer(step.photonBaseURL, err)
	}
	lookupResult.ProviderResponseBody = photonResponseBody
	switch {
	case photonResponseStatusCode == http.StatusTooManyRequests:
		return nil, render.PhotosPlaceLookupServiceIsLimitingRequests(step.photonBaseURL)
	case photonResponseStatusCode >= http.StatusInternalServerError:
		lookupResult.PlaceLookupOutcome = photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED
		lookupResult.PlaceLookupFailureMessage = fmt.Sprintf("Photon answered HTTP %d", photonResponseStatusCode)
		lookupResult.RetryNotBeforeTimeUtc = timestamppb.New(time.Now().Add(photonServerErrorRetryDelay))
		return lookupResult, nil
	case photonResponseStatusCode == http.StatusBadRequest:
		lookupResult.PlaceLookupOutcome = photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED
		lookupResult.PlaceLookupFailureMessage = fmt.Sprintf("Photon refused the request with HTTP %d", photonResponseStatusCode)
		return lookupResult, nil
	case photonResponseStatusCode != http.StatusOK:
		return nil, render.PhotosPlaceLookupServiceDidNotAnswer(step.photonBaseURL, fmt.Errorf("HTTP %d", photonResponseStatusCode))
	}
	nearbyPlacesNearestFirst, err := nearbyPlacesFromPhotonResponseBody(
		photonResponseBody,
		lookUpPlacesNearSpotRequest.GetCameraSpotLatitude(),
		lookUpPlacesNearSpotRequest.GetCameraSpotLongitude(),
	)
	if err != nil {
		lookupResult.PlaceLookupOutcome = photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED
		lookupResult.PlaceLookupFailureMessage = err.Error()
		return lookupResult, nil
	}
	lookupResult.NearbyPlacesNearestFirst = nearbyPlacesNearestFirst
	lookupResult.PlaceLookupOutcome = photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_PLACES_FOUND
	if len(nearbyPlacesNearestFirst) == 0 {
		lookupResult.PlaceLookupOutcome = photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_NO_PLACES
	}
	return lookupResult, nil
}

func (step *LookUpPlacesNearSpotStep) sendPacedPhotonRequest(ctx context.Context, photonRequestURL string) (int, []byte, error) {
	if waitBeforeRequest := step.minimumTimeBetweenPhotonRequests - time.Since(step.lastPhotonRequestTime); waitBeforeRequest > 0 {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(waitBeforeRequest):
		}
	}
	step.lastPhotonRequestTime = time.Now()
	photonRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, photonRequestURL, nil)
	if err != nil {
		return 0, nil, err
	}
	photonRequest.Header.Set("User-Agent", photonUserAgent)
	photonRequest.Header.Set("Accept", "application/json")
	photonResponse, err := step.photonHTTPClient.Do(photonRequest)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = photonResponse.Body.Close() }()
	photonResponseBody, err := io.ReadAll(photonResponse.Body)
	if err != nil {
		return 0, nil, err
	}
	return photonResponse.StatusCode, photonResponseBody, nil
}
