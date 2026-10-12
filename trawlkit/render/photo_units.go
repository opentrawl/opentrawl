package render

import (
	"fmt"
	"math"
	"strings"
)

func photoRoundedDecimalForHumanOutput(number float64, decimalPlaces int) string {
	if decimalPlaces == 0 {
		return FormatInteger(int64(math.Round(number)))
	}
	decimalScale := math.Pow10(decimalPlaces)
	rounded := math.Round(number*decimalScale) / decimalScale
	wholeNumber := int64(rounded)
	wholeNumberText := FormatInteger(wholeNumber)
	if rounded < 0 && wholeNumber == 0 {
		wholeNumberText = "-0"
	}
	_, fractionalDigits, _ := strings.Cut(fmt.Sprintf("%.*f", decimalPlaces, rounded), ".")
	fractionalDigits = strings.TrimRight(fractionalDigits, "0")
	if fractionalDigits != "" {
		return wholeNumberText + "." + fractionalDigits
	}
	return wholeNumberText
}

func photoDistanceMetresForHumanOutput(metres float64) string {
	if math.Abs(metres) >= 1000 {
		return photoRoundedDecimalForHumanOutput(metres/1000, 1) + " km"
	}
	return photoRoundedDecimalForHumanOutput(metres, 0) + " m"
}

func photoAltitudeForHumanOutput(metres float64) string {
	return photoRoundedDecimalForHumanOutput(metres, 0) + " m"
}

func photoAreaForHumanOutput(squareMetres float64) string {
	if math.Abs(squareMetres) >= 1000000 {
		return photoRoundedDecimalForHumanOutput(squareMetres/1000000, 1) + " km²"
	}
	return photoRoundedDecimalForHumanOutput(squareMetres, 0) + " m²"
}

func photoFocalLengthForHumanOutput(millimetres float64) string {
	return photoRoundedDecimalForHumanOutput(millimetres, 1) + " mm"
}

func photoApertureForHumanOutput(fNumber float64) string {
	return "f/" + photoRoundedDecimalForHumanOutput(fNumber, 1)
}

func photoExposureTimeForHumanOutput(exposureSeconds float64) string {
	if exposureSeconds > 0 && exposureSeconds <= 0.5 {
		return fmt.Sprintf("1/%.0f s", math.Round(1/exposureSeconds))
	}
	return photoRoundedDecimalForHumanOutput(exposureSeconds, 1) + " s"
}

func photoDurationForHumanOutput(seconds float64) string {
	return photoRoundedDecimalForHumanOutput(seconds, 1) + " seconds"
}
