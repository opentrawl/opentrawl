package imagemetadata

import "strings"

func opaqueNamespace(key string) bool {
	trimmed := strings.Trim(key, "{}")
	if strings.HasPrefix(strings.ToLower(trimmed), "maker") || strings.EqualFold(trimmed, "FileContents") {
		return true
	}
	if !strings.HasPrefix(key, "{") {
		return false
	}
	known := map[string]bool{
		"Exif": true, "GPS": true, "IPTC": true, "TIFF": true, "PNG": true, "GIF": true,
		"JFIF": true, "HEIF": true, "HEICS": true,
	}
	return !known[trimmed]
}

func readableKey(key string) string {
	labels := map[string]string{
		"{Exif}":                   "EXIF",
		"{GPS}":                    "GPS",
		"{IPTC}":                   "IPTC",
		"{TIFF}":                   "TIFF",
		"{PNG}":                    "PNG",
		"{GIF}":                    "GIF",
		"ExposureTime":             "Exposure time",
		"FNumber":                  "Aperture",
		"FocalLength":              "Focal length",
		"FocalLenIn35mmFilm":       "35 mm focal length",
		"ISOSpeedRatings":          "ISO",
		"DateTimeOriginal":         "Original capture time",
		"DateTimeDigitized":        "Digitised time",
		"DateTime":                 "Recorded time",
		"OffsetTimeOriginal":       "Original capture timezone",
		"OffsetTimeDigitized":      "Digitised capture timezone",
		"OffsetTime":               "Recorded timezone",
		"Orientation":              "Orientation",
		"Flash":                    "Flash",
		"ColorSpace":               "Colour space",
		"ExposureMode":             "Exposure mode",
		"ExposureProgram":          "Exposure program",
		"ExposureBiasValue":        "Exposure compensation",
		"BrightnessValue":          "Brightness",
		"MeteringMode":             "Metering mode",
		"LightSource":              "Light source",
		"SensingMethod":            "Sensor",
		"WhiteBalance":             "White balance",
		"FileSource":               "Image source",
		"SceneType":                "Scene source",
		"CustomRendered":           "Image processing",
		"SceneCaptureType":         "Scene type",
		"GainControl":              "Gain control",
		"Contrast":                 "Contrast",
		"Saturation":               "Saturation",
		"Sharpness":                "Sharpness",
		"SubjectDistanceRange":     "Subject distance",
		"FocalPlaneResolutionUnit": "Focal-plane resolution unit",
		"CompositeImage":           "Composite image",
		"SensitivityType":          "Sensitivity type",
		"HPositioningError":        "Horizontal accuracy",
		"GPSHPositioningError":     "Horizontal accuracy",
		"Altitude":                 "Altitude",
		"Speed":                    "Speed",
		"Track":                    "Travel direction",
		"ImgDirection":             "Camera direction",
		"DestBearing":              "Destination bearing",
		"DestDistance":             "Destination distance",
		"Status":                   "Position status",
		"MeasureMode":              "Positioning mode",
		"GPSDifferential":          "Differential correction",
		"FileSize":                 "File size",
		"Make":                     "Camera make",
		"Model":                    "Camera model",
		"LensMake":                 "Lens make",
		"LensModel":                "Lens model",
		"Software":                 "Camera software",
		"HostComputer":             "Host device",
		"PixelWidth":               "Pixel width",
		"PixelHeight":              "Pixel height",
		"PixelXDimension":          "Recorded width",
		"PixelYDimension":          "Recorded height",
		"CanvasPixelWidth":         "Canvas width",
		"CanvasPixelHeight":        "Canvas height",
		"Depth":                    "Colour depth",
		"DPIWidth":                 "Horizontal resolution",
		"DPIHeight":                "Vertical resolution",
		"XPixelsPerMeter":          "Horizontal resolution",
		"YPixelsPerMeter":          "Vertical resolution",
		"XResolution":              "Horizontal resolution",
		"YResolution":              "Vertical resolution",
		"ResolutionUnit":           "Resolution unit",
		"TileWidth":                "Tile width",
		"TileLength":               "Tile height",
		"ProfileName":              "Colour profile",
		"ColorModel":               "Colour model",
		"LensSpecification":        "Lens specification",
		"Caption/Abstract":         "Caption",
		"UserComment":              "User comment",
		"InterlaceType":            "Interlacing",
	}
	if label, ok := labels[key]; ok {
		return label
	}
	return key
}
