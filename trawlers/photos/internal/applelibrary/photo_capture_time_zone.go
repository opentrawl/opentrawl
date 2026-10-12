package applelibrary

import (
	"fmt"
	"strconv"
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
		return DerivePhotoCaptureTimeZoneFromAppleDeviceName(captureLocalTime)
	}
	captureLocalTime.CaptureUtcOffsetSeconds = new(captureLocalTime.GetAppleDeviceUtcOffsetSeconds())
	captureLocalTime.CaptureTimeZoneName = captureLocalTime.GetAppleDeviceTimeZoneName()
	captureLocalTime.CaptureTimeZoneSource = photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_APPLE_DEVICE_UTC_OFFSET
	return nil
}

// DerivePhotoCaptureTimeZoneFromAppleDeviceName computes the capture offset
// without inventing an offset Apple did not record for the device.
func DerivePhotoCaptureTimeZoneFromAppleDeviceName(captureLocalTime *photo.PhotoCaptureLocalTime) error {
	appleDeviceTimeZoneName := captureLocalTime.GetAppleDeviceTimeZoneName()
	if appleDeviceTimeZoneName == "" {
		captureLocalTime.CaptureTimeZoneSource = photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_NOT_KNOWN
		return nil
	}
	appleDeviceTimeZone, err := loadAppleDeviceTimeZone(appleDeviceTimeZoneName)
	if err != nil {
		return err
	}
	_, captureUtcOffsetSeconds := captureLocalTime.GetCaptureTimeUtc().AsTime().In(appleDeviceTimeZone).Zone()
	captureLocalTime.CaptureUtcOffsetSeconds = new(int32(captureUtcOffsetSeconds))
	captureLocalTime.CaptureTimeZoneName = appleDeviceTimeZoneName
	captureLocalTime.CaptureTimeZoneSource = photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_APPLE_DEVICE_TIME_ZONE_NAME
	return nil
}

func loadAppleDeviceTimeZone(appleDeviceTimeZoneName string) (*time.Location, error) {
	// Apple writes fixed zones as GMT+HHMM or GMT-HHMM; Go's zone database
	// contains GMT itself but does not contain these names.
	if len(appleDeviceTimeZoneName) == 8 && strings.HasPrefix(appleDeviceTimeZoneName, "GMT") &&
		(appleDeviceTimeZoneName[3] == '+' || appleDeviceTimeZoneName[3] == '-') {
		hours, hoursError := strconv.Atoi(appleDeviceTimeZoneName[4:6])
		minutes, minutesError := strconv.Atoi(appleDeviceTimeZoneName[6:8])
		if hoursError != nil || minutesError != nil || hours > 23 || minutes > 59 || hours < 0 || minutes < 0 {
			return nil, fmt.Errorf("apple device time zone %s has an invalid fixed offset", appleDeviceTimeZoneName)
		}
		offsetSeconds := hours*3600 + minutes*60
		if appleDeviceTimeZoneName[3] == '-' {
			offsetSeconds = -offsetSeconds
		}
		return time.FixedZone(appleDeviceTimeZoneName, offsetSeconds), nil
	}
	appleDeviceTimeZone, err := time.LoadLocation(appleDeviceTimeZoneName)
	if err != nil {
		return nil, fmt.Errorf("load Apple device time zone %s: %w", appleDeviceTimeZoneName, err)
	}
	return appleDeviceTimeZone, nil
}
