package applelibrary

import (
	"encoding/binary"
	"fmt"
	"math"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// Apple stores a location as eight little-endian doubles, in the order of
// CoreLocation's CLLocation: latitude, longitude, altitude, horizontal
// accuracy, vertical accuracy, course, speed and time.
const (
	appleLocationDataByteCount           = 64
	appleLocationLatitudeIndex           = 0
	appleLocationLongitudeIndex          = 1
	appleLocationAltitudeIndex           = 2
	appleLocationHorizontalAccuracyIndex = 3
	appleLocationDoubleCount             = appleLocationDataByteCount / 8
)

func decodeAppleLocationDoubles(locationData []byte) ([appleLocationDoubleCount]float64, error) {
	var locationDoubles [appleLocationDoubleCount]float64
	if len(locationData) != appleLocationDataByteCount {
		return locationDoubles, fmt.Errorf("location data has %d bytes, not %d", len(locationData), appleLocationDataByteCount)
	}
	for index := range locationDoubles {
		locationDoubles[index] = math.Float64frombits(binary.LittleEndian.Uint64(locationData[index*8:]))
	}
	return locationDoubles, nil
}

// decodeAppleLocationData returns nil when the photo has no location. Apple
// writes 0 for an accuracy or altitude it does not know; those stay unset.
func decodeAppleLocationData(locationData []byte) (*photo.PhotoCameraLocation, error) {
	if len(locationData) == 0 {
		return nil, nil
	}
	locationDoubles, err := decodeAppleLocationDoubles(locationData)
	if err != nil {
		return nil, err
	}
	cameraLocation := &photo.PhotoCameraLocation{
		CameraLatitude:  locationDoubles[appleLocationLatitudeIndex],
		CameraLongitude: locationDoubles[appleLocationLongitudeIndex],
	}
	if altitude := locationDoubles[appleLocationAltitudeIndex]; altitude != 0 {
		cameraLocation.CameraAltitudeMetres = &altitude
	}
	if horizontalAccuracy := locationDoubles[appleLocationHorizontalAccuracyIndex]; horizontalAccuracy > 0 {
		cameraLocation.CameraHorizontalAccuracyMetres = &horizontalAccuracy
	}
	return cameraLocation, nil
}

func decodeAppleChinaShiftedLocationData(shiftedLocationData []byte) (*photo.AppleChinaShiftedLocation, error) {
	locationDoubles, err := decodeAppleLocationDoubles(shiftedLocationData)
	if err != nil {
		return nil, err
	}
	return &photo.AppleChinaShiftedLocation{
		AppleChinaShiftedLatitude:  locationDoubles[appleLocationLatitudeIndex],
		AppleChinaShiftedLongitude: locationDoubles[appleLocationLongitudeIndex],
	}, nil
}
