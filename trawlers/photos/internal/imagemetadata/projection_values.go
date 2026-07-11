package imagemetadata

import (
	"strconv"
	"strings"
)

func projectScalar(key string, value Value) (string, bool) {
	switch value.Type {
	case ValueBoolean:
		if !knownBooleanKey(key) {
			return "", false
		}
		if value.Boolean != nil && *value.Boolean {
			return "yes", true
		}
		return "no", true
	case ValueSigned:
		return projectInteger(key, stringValue(value.Signed))
	case ValueUnsigned:
		return projectInteger(key, stringValue(value.Unsigned))
	case ValueDecimal:
		return projectNumber(key, stringValue(value.Decimal))
	case ValueDate:
		return dateValue(stringValue(value.Date)), true
	default:
		return "", false
	}
}

func projectInteger(key, raw string) (string, bool) {
	switch key {
	case "FileSize":
		return humanBytes(raw), true
	case "PixelWidth", "PixelHeight", "CanvasPixelWidth", "CanvasPixelHeight", "PixelXDimension", "PixelYDimension", "TileWidth", "TileLength":
		return raw + " pixels", true
	case "Depth":
		return raw + " bits", true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", false
	}
	if rendered, ok := projectNumeric(key, float64(value)); ok {
		return rendered, true
	}
	switch key {
	case "Orientation":
		if label, ok := orientationLabel(value); ok {
			return label, true
		}
	case "ColorSpace":
		switch value {
		case 1:
			return "sRGB", true
		case 65535:
			return "uncalibrated", true
		}
	case "ExposureMode":
		if label, ok := enumLabel(value, []string{"automatic", "manual", "automatic bracket"}); ok {
			return label, true
		}
	case "WhiteBalance":
		if value == 0 {
			return "automatic", true
		}
		if value == 1 {
			return "manual", true
		}
	case "Flash":
		return flashLabel(value), true
	case "ResolutionUnit":
		if value == 2 {
			return "inches", true
		}
		if value == 3 {
			return "centimetres", true
		}
	case "InterlaceType":
		if value == 0 {
			return "none", true
		}
		if value == 1 {
			return "Adam7", true
		}
	case "ExposureProgram":
		return mapEnum(value, map[int64]string{0: "not defined", 1: "manual", 2: "normal", 3: "aperture priority", 4: "shutter priority", 5: "creative", 6: "action", 7: "portrait", 8: "landscape"})
	case "MeteringMode":
		return mapEnum(value, map[int64]string{0: "unknown", 1: "average", 2: "centre-weighted average", 3: "spot", 4: "multi-spot", 5: "pattern", 6: "partial", 255: "other"})
	case "LightSource":
		return mapEnum(value, exifLightSources)
	case "SensingMethod":
		return mapEnum(value, map[int64]string{1: "not defined", 2: "one-chip colour area sensor", 3: "two-chip colour area sensor", 4: "three-chip colour area sensor", 5: "colour sequential area sensor", 7: "trilinear sensor", 8: "colour sequential linear sensor"})
	case "FileSource":
		return mapEnum(value, map[int64]string{3: "digital still camera"})
	case "SceneType":
		return mapEnum(value, map[int64]string{1: "directly photographed"})
	case "CustomRendered":
		return mapEnum(value, map[int64]string{0: "standard processing", 1: "custom processing"})
	case "SceneCaptureType":
		return mapEnum(value, map[int64]string{0: "standard", 1: "landscape", 2: "portrait", 3: "night scene"})
	case "GainControl":
		return mapEnum(value, map[int64]string{0: "none", 1: "low gain up", 2: "high gain up", 3: "low gain down", 4: "high gain down"})
	case "Contrast":
		return mapEnum(value, map[int64]string{0: "normal", 1: "soft", 2: "hard"})
	case "Saturation":
		return mapEnum(value, map[int64]string{0: "normal", 1: "low", 2: "high"})
	case "Sharpness":
		return mapEnum(value, map[int64]string{0: "normal", 1: "soft", 2: "hard"})
	case "SubjectDistanceRange":
		return mapEnum(value, map[int64]string{0: "unknown", 1: "macro", 2: "close", 3: "distant view"})
	case "FocalPlaneResolutionUnit":
		return mapEnum(value, map[int64]string{2: "inches", 3: "centimetres"})
	case "CompositeImage":
		return mapEnum(value, map[int64]string{0: "not composite", 1: "general composite", 2: "composite captured during shooting"})
	case "SensitivityType":
		return mapEnum(value, map[int64]string{0: "unknown", 1: "standard output sensitivity", 2: "recommended exposure index", 3: "ISO speed", 4: "standard output sensitivity and recommended exposure index", 5: "standard output sensitivity and ISO speed", 6: "recommended exposure index and ISO speed", 7: "standard output sensitivity, recommended exposure index and ISO speed"})
	case "GPSDifferential":
		return mapEnum(value, map[int64]string{0: "no differential correction", 1: "differential correction applied"})
	case "AltitudeRef":
		return mapEnum(value, map[int64]string{0: "above sea level", 1: "below sea level"})
	case "PhotographicSensitivity", "StandardOutputSensitivity", "RecommendedExposureIndex", "ISOSpeed", "ISOSpeedLatitudeyyy", "ISOSpeedLatitudezzz":
		if value >= 0 {
			return raw, true
		}
	}
	return "", false
}

func projectNumber(key, raw string) (string, bool) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", false
	}
	return projectNumeric(key, value)
}

func projectNumeric(key string, value float64) (string, bool) {
	switch key {
	case "ExposureTime":
		return shutterSpeed(value), true
	case "FNumber":
		return "f/" + significant(value, 2), true
	case "FocalLength":
		return significant(value, 3) + " mm", true
	case "FocalLenIn35mmFilm", "FocalLengthIn35mmFilm":
		return significant(value, 3) + " mm equivalent", true
	case "GPSHPositioningError", "HPositioningError", "HorizontalAccuracy":
		return formatDistance(value), true
	case "GPSAltitude", "Altitude", "SubjectDistance":
		return significant(value, 4) + " m", true
	case "ExposureBiasValue", "BrightnessValue", "ShutterSpeedValue", "ApertureValue", "MaxApertureValue":
		return significant(value, 4) + " EV", true
	case "DPIWidth", "DPIHeight":
		return significant(value, 4) + " dpi", true
	case "XPixelsPerMeter", "YPixelsPerMeter":
		return significant(value, 6) + " pixels per metre", true
	case "DigitalZoomRatio":
		return significant(value, 3) + "×", true
	case "Gamma":
		return significant(value, 4), true
	}
	return "", false
}

func projectString(key, raw string) (string, bool) {
	switch key {
	case "LatitudeRef":
		return mapStringEnum(raw, map[string]string{"N": "north", "S": "south"})
	case "LongitudeRef":
		return mapStringEnum(raw, map[string]string{"E": "east", "W": "west"})
	case "AltitudeRef":
		return mapStringEnum(raw, map[string]string{"0": "above sea level", "1": "below sea level"})
	case "Status":
		return mapStringEnum(raw, map[string]string{"A": "measurement active", "V": "measurement void"})
	case "MeasureMode":
		return mapStringEnum(raw, map[string]string{"2": "2D positioning", "3": "3D positioning"})
	case "SpeedRef":
		return gpsSpeedUnitLabel(raw)
	case "TrackRef", "ImgDirectionRef", "DestBearingRef":
		return gpsDirectionUnitLabel(raw)
	case "DestDistanceRef":
		return gpsDistanceUnitLabel(raw)
	case "OffsetTime", "OffsetTimeOriginal", "OffsetTimeDigitized":
		if _, ok := offsetSeconds(raw); ok {
			return raw, true
		}
		return "", false
	}
	if knownTextKey(key) {
		return raw, true
	}
	return "", false
}

func knownBooleanKey(key string) bool {
	switch key {
	case "HasAlpha", "IsFloat", "IsIndexed", "HasThumbnail", "HasThumbnails":
		return true
	default:
		return false
	}
}

func knownTextKey(key string) bool {
	switch key {
	case "FileType", "FileTypeExtension", "ColorModel", "ProfileName",
		"Make", "Model", "Software", "Artist", "ImageDescription", "Copyright", "HostComputer",
		"LensMake", "LensModel", "UserComment",
		"Caption/Abstract", "CaptionAbstract", "ObjectName", "Headline", "Credit", "Source", "Byline",
		"City", "SubLocation", "Province/State", "Country/PrimaryLocationName", "CountryCode", "Instructions":
		return true
	default:
		return false
	}
}

func integerField(value Value) (int64, bool) {
	var raw string
	switch value.Type {
	case ValueSigned:
		raw = stringValue(value.Signed)
	case ValueUnsigned:
		raw = stringValue(value.Unsigned)
	default:
		return 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	return parsed, err == nil
}

func enumLabel(value int64, labels []string) (string, bool) {
	if value < 0 || value >= int64(len(labels)) {
		return "", false
	}
	return labels[value], true
}

func mapEnum(value int64, labels map[int64]string) (string, bool) {
	label, ok := labels[value]
	return label, ok
}

func mapStringEnum(value string, labels map[string]string) (string, bool) {
	label, ok := labels[strings.ToUpper(strings.TrimSpace(value))]
	return label, ok
}

func gpsSpeedUnit(reference string) (string, bool) {
	label, ok := mapStringEnum(reference, map[string]string{"K": " km/h", "M": " mph", "N": " knots"})
	return label, ok
}

func gpsSpeedUnitLabel(reference string) (string, bool) {
	return mapStringEnum(reference, map[string]string{"K": "kilometres per hour", "M": "miles per hour", "N": "knots"})
}

func gpsDirectionUnit(reference string) (string, bool) {
	label, ok := mapStringEnum(reference, map[string]string{"T": "° true", "M": "° magnetic"})
	return label, ok
}

func gpsDirectionUnitLabel(reference string) (string, bool) {
	return mapStringEnum(reference, map[string]string{"T": "true direction", "M": "magnetic direction"})
}

func gpsDistanceUnit(reference string) (string, bool) {
	label, ok := mapStringEnum(reference, map[string]string{"K": " km", "M": " miles", "N": " nautical miles"})
	return label, ok
}

func gpsDistanceUnitLabel(reference string) (string, bool) {
	return mapStringEnum(reference, map[string]string{"K": "kilometres", "M": "miles", "N": "nautical miles"})
}

var exifLightSources = map[int64]string{
	0: "unknown", 1: "daylight", 2: "fluorescent", 3: "tungsten", 4: "flash",
	9: "fine weather", 10: "cloudy", 11: "shade", 12: "daylight fluorescent",
	13: "day white fluorescent", 14: "cool white fluorescent", 15: "white fluorescent",
	17: "standard light A", 18: "standard light B", 19: "standard light C", 20: "D55",
	21: "D65", 22: "D75", 23: "D50", 24: "ISO studio tungsten", 255: "other",
}
