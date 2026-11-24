// Package placelookup is step 3, look_up_places_near_spot: one Photon reverse
// geocoding request per camera spot, returning the named OpenStreetMap places
// around it, nearest first.
package placelookup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/proto"
)

// PublicPhotonBaseURL is Komoot's public Photon service, used when the Photos
// configuration names no other Photon service.
const PublicPhotonBaseURL = "https://photon.komoot.io"

// Photon returns at most 50 results from a reverse request, nearest first. A
// 5 km radius costs nothing where places are dense, because the 50 nearest are
// close, and reaches distant peaks, bridges and lighthouses where places are
// sparse.
const (
	photonReverseResultLimit      = "50"
	photonReverseRadiusKilometres = "5"
)

// Bare house numbers and minor roads for motor vehicles fill most results in
// residential streets. Apple's stored place already names the street the
// camera stood on. Paths, tracks, footways, steps and major roads stay: people
// photograph trails, and some major roads are landmarks, such as a bridge
// that OpenStreetMap tags highway:primary.
var photonReverseExcludedOpenStreetMapTags = []string{
	"!place:house",
	"!place:postcode",
	"!highway:residential",
	"!highway:service",
	"!highway:unclassified",
	"!highway:living_street",
}

// PhotonReverseLookupRequestForCameraSpot makes the exact request for one spot.
// Changing the request parameters changes the request text, so every spot is
// looked up again with the new request.
func PhotonReverseLookupRequestForCameraSpot(
	cameraSpotKey string,
	cameraSpotLatitude, cameraSpotLongitude float64,
) *photosstep.LookUpPlacesNearSpotRequest {
	return &photosstep.LookUpPlacesNearSpotRequest{
		PlaceProvider:          photo.PlaceProvider_PLACE_PROVIDER_OPENSTREETMAP_PHOTON,
		CameraSpotKey:          cameraSpotKey,
		CameraSpotLatitude:     cameraSpotLatitude,
		CameraSpotLongitude:    cameraSpotLongitude,
		PlaceLookupRequestText: photonReversePathAndQuery(cameraSpotLatitude, cameraSpotLongitude),
	}
}

func photonReversePathAndQuery(cameraSpotLatitude, cameraSpotLongitude float64) string {
	query := "lat=" + strconv.FormatFloat(cameraSpotLatitude, 'f', 4, 64) +
		"&lon=" + strconv.FormatFloat(cameraSpotLongitude, 'f', 4, 64) +
		"&limit=" + photonReverseResultLimit +
		"&radius=" + photonReverseRadiusKilometres +
		"&lang=en"
	for _, excludedTag := range photonReverseExcludedOpenStreetMapTags {
		query += "&osm_tag=" + excludedTag
	}
	return "/reverse?" + query
}

// PlaceLookupRequestSHA256 identifies one exact request. A lookup is stored
// under it and is never sent again once it has an answer.
func PlaceLookupRequestSHA256(lookUpPlacesNearSpotRequest *photosstep.LookUpPlacesNearSpotRequest) (string, error) {
	encodedRequest, err := proto.MarshalOptions{Deterministic: true}.Marshal(lookUpPlacesNearSpotRequest)
	if err != nil {
		return "", fmt.Errorf("encode place lookup request: %w", err)
	}
	requestSHA256 := sha256.Sum256(encodedRequest)
	return hex.EncodeToString(requestSHA256[:]), nil
}
