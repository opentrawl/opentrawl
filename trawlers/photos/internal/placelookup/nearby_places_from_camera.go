package placelookup

import (
	"math"
	"sort"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// NearbyPlacesFromCameraNearestFirst measures each stored place from where
// one photo's camera stood. A spot's places were found around the spot, so
// they are sorted again by their distance from this camera.
func NearbyPlacesFromCameraNearestFirst(
	cameraLocation *photo.PhotoCameraLocation,
	nearbyPlaces []*photo.NearbyPlace,
) []*photo.NearbyPlaceFromCamera {
	nearbyPlacesFromCamera := make([]*photo.NearbyPlaceFromCamera, 0, len(nearbyPlaces))
	for _, nearbyPlace := range nearbyPlaces {
		nearbyPlacesFromCamera = append(nearbyPlacesFromCamera, &photo.NearbyPlaceFromCamera{
			NearbyPlace: nearbyPlace,
			DistanceFromCameraMetres: greatCircleDistanceMetres(
				cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude(),
				nearbyPlace.GetPlaceLatitude(), nearbyPlace.GetPlaceLongitude()),
			DirectionFromCameraDegrees: initialCompassDirectionDegrees(
				cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude(),
				nearbyPlace.GetPlaceLatitude(), nearbyPlace.GetPlaceLongitude()),
		})
	}
	sort.SliceStable(nearbyPlacesFromCamera, func(left, right int) bool {
		return nearbyPlacesFromCamera[left].GetDistanceFromCameraMetres() < nearbyPlacesFromCamera[right].GetDistanceFromCameraMetres()
	})
	return nearbyPlacesFromCamera
}

// initialCompassDirectionDegrees is the direction to set off in along the
// shortest path, in degrees clockwise from true north.
func initialCompassDirectionDegrees(fromLatitude, fromLongitude, toLatitude, toLongitude float64) float64 {
	fromLatitudeRadians := fromLatitude * math.Pi / 180
	toLatitudeRadians := toLatitude * math.Pi / 180
	longitudeDifferenceRadians := (toLongitude - fromLongitude) * math.Pi / 180
	eastward := math.Sin(longitudeDifferenceRadians) * math.Cos(toLatitudeRadians)
	northward := math.Cos(fromLatitudeRadians)*math.Sin(toLatitudeRadians) -
		math.Sin(fromLatitudeRadians)*math.Cos(toLatitudeRadians)*math.Cos(longitudeDifferenceRadians)
	return math.Mod(math.Atan2(eastward, northward)*180/math.Pi+360, 360)
}
