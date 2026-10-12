package applelibrary

import (
	"fmt"
	"strings"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/ringsaturn/tzf"
)

// Nautical map zones do not establish the camera clock offset.
const timeZoneMapNauticalZonePrefix = "Etc/"

// setPhotoCaptureTimeZone sets the zone where the photo was taken: the
// time-zone map's zone at the camera position, because Apple's device offset
// follows the camera clock, which lags on travel days. With no position, or
// a position at sea, Apple's device offset is the best evidence and is used
// as it is.
func setPhotoCaptureTimeZone(
	captureLocalTime *photo.PhotoCaptureLocalTime,
	cameraLocation *photo.PhotoCameraLocation,
	timeZoneMap tzf.F,
) error {
	if cameraLocation != nil {
		timeZoneName := timeZoneMap.GetTimezoneName(cameraLocation.GetCameraLongitude(), cameraLocation.GetCameraLatitude())
		if timeZoneName == "" {
			return fmt.Errorf("the time-zone map has no zone at %f, %f", cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude())
		}
		if !strings.HasPrefix(timeZoneName, timeZoneMapNauticalZonePrefix) {
			timeZone, err := time.LoadLocation(timeZoneName)
			if err != nil {
				return fmt.Errorf("load time zone %s from the time-zone map: %w", timeZoneName, err)
			}
			_, captureUtcOffsetSeconds := captureLocalTime.GetCaptureTimeUtc().AsTime().In(timeZone).Zone()
			captureLocalTime.CaptureUtcOffsetSeconds = new(int32(captureUtcOffsetSeconds))
			captureLocalTime.CaptureTimeZoneName = timeZoneName
			captureLocalTime.CaptureTimeZoneSource = photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_TIME_ZONE_MAP_AT_CAMERA_LOCATION
			return nil
		}
	}
	if captureLocalTime.AppleDeviceUtcOffsetSeconds == nil {
		captureLocalTime.CaptureTimeZoneSource = photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_NOT_KNOWN
		return nil
	}
	captureLocalTime.CaptureUtcOffsetSeconds = new(captureLocalTime.GetAppleDeviceUtcOffsetSeconds())
	captureLocalTime.CaptureTimeZoneName = captureLocalTime.GetAppleDeviceTimeZoneName()
	captureLocalTime.CaptureTimeZoneSource = photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_APPLE_DEVICE_UTC_OFFSET
	return nil
}
