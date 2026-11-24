package placelookup

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// photonReverseResponseBody is the part of Photon's GeoJSON answer that
// becomes nearby places. The full answer is kept as the provider response
// body.
type photonReverseResponseBody struct {
	Type     string `json:"type"`
	Features []struct {
		Geometry struct {
			// GeoJSON order: longitude, then latitude.
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties struct {
			Name               string `json:"name"`
			OpenStreetMapType  string `json:"osm_type"`
			OpenStreetMapID    int64  `json:"osm_id"`
			OpenStreetMapKey   string `json:"osm_key"`
			OpenStreetMapValue string `json:"osm_value"`
		} `json:"properties"`
	} `json:"features"`
}

type nearbyPlaceWithDistanceFromSpot struct {
	nearbyPlace            *photo.NearbyPlace
	distanceFromSpotMetres float64
}

const sameNamedPlaceCollapseDistanceMetres = 50.0

func placeKindIsLinearFeature(openStreetMapKey string) bool {
	switch openStreetMapKey {
	case "highway", "railway", "waterway", "route":
		return true
	default:
		return false
	}
}

// nearbyPlacesFromPhotonResponseBody keeps each named place once, nearest
// first. Repeated entries for the same OpenStreetMap object (same osm_type and
// osm_id) collapse to their nearest point, as do segments of one named linear
// feature (trail, road, rail, waterway) or identically named places within 50 m.
// Distinct landmarks further apart (such as two separate monuments or bunkers)
// stay separate so the card model can tell which one is in frame.
func nearbyPlacesFromPhotonResponseBody(
	photonResponseBody []byte,
	cameraSpotLatitude, cameraSpotLongitude float64,
) ([]*photo.NearbyPlace, error) {
	var photonResponse photonReverseResponseBody
	if err := json.Unmarshal(photonResponseBody, &photonResponse); err != nil {
		return nil, fmt.Errorf("the Photon answer is not GeoJSON: %w", err)
	}
	if photonResponse.Type != "FeatureCollection" || photonResponse.Features == nil {
		return nil, fmt.Errorf("the Photon answer is not a GeoJSON FeatureCollection")
	}
	type candidatePlace struct {
		nearbyPlace            *photo.NearbyPlace
		distanceFromSpotMetres float64
		openStreetMapType      string
		openStreetMapID        int64
		openStreetMapKey       string
		normalizedNameAndKind  string
	}
	candidates := make([]candidatePlace, 0, len(photonResponse.Features))
	for _, feature := range photonResponse.Features {
		placeName := strings.TrimSpace(feature.Properties.Name)
		if placeName == "" {
			continue
		}
		if len(feature.Geometry.Coordinates) != 2 {
			return nil, fmt.Errorf("the Photon place %q has %d coordinates, not 2", placeName, len(feature.Geometry.Coordinates))
		}
		nearbyPlace := &photo.NearbyPlace{
			PlaceName:      placeName,
			PlaceKind:      feature.Properties.OpenStreetMapKey + ":" + feature.Properties.OpenStreetMapValue,
			PlaceLatitude:  feature.Geometry.Coordinates[1],
			PlaceLongitude: feature.Geometry.Coordinates[0],
		}
		distanceFromSpotMetres := greatCircleDistanceMetres(
			cameraSpotLatitude, cameraSpotLongitude, nearbyPlace.PlaceLatitude, nearbyPlace.PlaceLongitude)
		candidates = append(candidates, candidatePlace{
			nearbyPlace:            nearbyPlace,
			distanceFromSpotMetres: distanceFromSpotMetres,
			openStreetMapType:      feature.Properties.OpenStreetMapType,
			openStreetMapID:        feature.Properties.OpenStreetMapID,
			openStreetMapKey:       feature.Properties.OpenStreetMapKey,
			normalizedNameAndKind:  strings.ToLower(strings.Join(strings.Fields(placeName), " ")) + "\x00" + nearbyPlace.PlaceKind,
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].distanceFromSpotMetres != candidates[right].distanceFromSpotMetres {
			return candidates[left].distanceFromSpotMetres < candidates[right].distanceFromSpotMetres
		}
		if candidates[left].nearbyPlace.PlaceName != candidates[right].nearbyPlace.PlaceName {
			return candidates[left].nearbyPlace.PlaceName < candidates[right].nearbyPlace.PlaceName
		}
		if candidates[left].nearbyPlace.PlaceKind != candidates[right].nearbyPlace.PlaceKind {
			return candidates[left].nearbyPlace.PlaceKind < candidates[right].nearbyPlace.PlaceKind
		}
		return candidates[left].openStreetMapID < candidates[right].openStreetMapID
	})
	seenOpenStreetMapObjects := map[string]bool{}
	keptByNameAndKind := map[string][]*photo.NearbyPlace{}
	keptPlaces := make([]nearbyPlaceWithDistanceFromSpot, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.openStreetMapID != 0 {
			objectKey := fmt.Sprintf("%s:%d", candidate.openStreetMapType, candidate.openStreetMapID)
			if seenOpenStreetMapObjects[objectKey] {
				continue
			}
			seenOpenStreetMapObjects[objectKey] = true
		}
		existingSameNameAndKind := keptByNameAndKind[candidate.normalizedNameAndKind]
		if len(existingSameNameAndKind) > 0 {
			if placeKindIsLinearFeature(candidate.openStreetMapKey) {
				continue
			}
			isDuplicateNearbyPoint := false
			for _, kept := range existingSameNameAndKind {
				if greatCircleDistanceMetres(
					kept.PlaceLatitude, kept.PlaceLongitude,
					candidate.nearbyPlace.PlaceLatitude, candidate.nearbyPlace.PlaceLongitude,
				) <= sameNamedPlaceCollapseDistanceMetres {
					isDuplicateNearbyPoint = true
					break
				}
			}
			if isDuplicateNearbyPoint {
				continue
			}
		}
		keptByNameAndKind[candidate.normalizedNameAndKind] = append(existingSameNameAndKind, candidate.nearbyPlace)
		keptPlaces = append(keptPlaces, nearbyPlaceWithDistanceFromSpot{
			nearbyPlace:            candidate.nearbyPlace,
			distanceFromSpotMetres: candidate.distanceFromSpotMetres,
		})
	}
	nearbyPlacesNearestFirst := make([]*photo.NearbyPlace, 0, len(keptPlaces))
	for _, keptPlace := range keptPlaces {
		nearbyPlacesNearestFirst = append(nearbyPlacesNearestFirst, keptPlace.nearbyPlace)
	}
	return nearbyPlacesNearestFirst, nil
}

const earthMeanRadiusMetres = 6371008.8

func greatCircleDistanceMetres(fromLatitude, fromLongitude, toLatitude, toLongitude float64) float64 {
	fromLatitudeRadians := fromLatitude * math.Pi / 180
	toLatitudeRadians := toLatitude * math.Pi / 180
	latitudeDifferenceRadians := (toLatitude - fromLatitude) * math.Pi / 180
	longitudeDifferenceRadians := (toLongitude - fromLongitude) * math.Pi / 180
	haversine := math.Sin(latitudeDifferenceRadians/2)*math.Sin(latitudeDifferenceRadians/2) +
		math.Cos(fromLatitudeRadians)*math.Cos(toLatitudeRadians)*
			math.Sin(longitudeDifferenceRadians/2)*math.Sin(longitudeDifferenceRadians/2)
	return 2 * earthMeanRadiusMetres * math.Asin(math.Sqrt(haversine))
}
