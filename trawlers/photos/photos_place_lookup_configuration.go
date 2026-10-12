package photos

import (
	"errors"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

func (c *Crawler) placeLookupRequestParameters() ([]*photosstep.PhotosPlaceLookupRequestParameters, error) {
	photosConfiguration := c.photosConfiguration
	if photosConfiguration.AppleMapSearchRadiusMetres == 0 && photosConfiguration.AppleMapSearchQuery == "" &&
		photosConfiguration.OpenStreetMapSearchRadiusMetres == 0 && photosConfiguration.OpenStreetMapSearchQuery == "" {
		return nil, nil
	}
	if photosConfiguration.AppleMapSearchRadiusMetres <= 0 || photosConfiguration.OpenStreetMapSearchRadiusMetres <= 0 {
		return nil, errors.New("both Apple Maps and OpenStreetMap place search radii must be configured and greater than zero")
	}
	return []*photosstep.PhotosPlaceLookupRequestParameters{
		{
			PlaceProvider:           photo.PlaceProvider_PLACE_PROVIDER_APPLE_MAP_POINTS_OF_INTEREST,
			PlaceSearchRadiusMetres: photosConfiguration.AppleMapSearchRadiusMetres,
			PlaceSearchQuery:        photosConfiguration.AppleMapSearchQuery,
		},
		{
			PlaceProvider:           photo.PlaceProvider_PLACE_PROVIDER_OPENSTREETMAP_OVERPASS,
			PlaceSearchRadiusMetres: photosConfiguration.OpenStreetMapSearchRadiusMetres,
			PlaceSearchQuery:        photosConfiguration.OpenStreetMapSearchQuery,
		},
	}, nil
}
