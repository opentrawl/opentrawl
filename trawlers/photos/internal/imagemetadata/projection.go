package imagemetadata

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Project(record Record) Projection {
	projection := Projection{
		ExtractorVersion: record.ExtractorVersion,
		OriginalSHA256:   record.OriginalSHA256,
		Lines:            []string{},
		Exclusions:       []Exclusion{},
	}
	writer := projectionWriter{projection: &projection}
	writer.value("Container", "container", record.Container)
	for _, image := range record.Images {
		writer.value(fmt.Sprintf("Image %d", image.Index+1), fmt.Sprintf("image[%d]", image.Index), image.Properties)
	}
	return projection
}

type projectionWriter struct {
	projection *Projection
}

func (w projectionWriter) value(label, path string, value Value) {
	switch value.Type {
	case ValueDictionary:
		w.dictionary(label, path, value.Dictionary)
	case ValueArray:
		w.array(label, path, value.Array)
	case ValueNull:
		w.exclude(path, "null value")
	case ValueData:
		decoded, _ := base64.StdEncoding.DecodeString(stringValue(value.Data))
		w.exclude(path, fmt.Sprintf("binary data (%d bytes)", len(decoded)))
	case ValueString:
		if stringValue(value.String) == "" {
			w.exclude(path, "empty string")
			return
		}
		if rendered, ok := projectString(lastPathPart(path), stringValue(value.String)); ok {
			w.line(label, rendered)
			return
		}
		w.excludeUnrecognised(path)
	default:
		if rendered, ok := projectScalar(lastPathPart(path), value); ok {
			w.line(label, rendered)
			return
		}
		w.excludeUnrecognised(path)
	}
}

func (w projectionWriter) dictionary(label, path string, values map[string]Value) {
	handled := map[string]bool{}
	w.gps(label, path, values, handled)
	w.dates(label, path, values, handled)
	w.resolution(label, path, values, handled)
	w.knownArrays(label, path, values, handled)
	w.redundantAPEX(label, path, values, handled)

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if handled[key] {
			continue
		}
		childPath := path + "." + key
		child := values[key]
		if opaqueNamespace(key) {
			w.excludeOpaque(childPath, child)
			continue
		}
		w.value(joinLabel(label, readableKey(key)), childPath, child)
	}
}

func (w projectionWriter) array(label, path string, values []Value) {
	for index, value := range values {
		itemLabel := fmt.Sprintf("%s %d", label, index+1)
		if len(values) == 1 {
			itemLabel = label
		}
		w.value(itemLabel, fmt.Sprintf("%s[%d]", path, index), value)
	}
}

func (w projectionWriter) dates(label, path string, values map[string]Value, handled map[string]bool) {
	for _, pair := range []struct {
		dateKey   string
		subsecKey string
		offsetKey string
	}{
		{"DateTimeOriginal", "SubsecTimeOriginal", "OffsetTimeOriginal"},
		{"DateTimeDigitized", "SubsecTimeDigitized", "OffsetTimeDigitized"},
		{"DateTime", "SubsecTime", "OffsetTime"},
	} {
		date, ok := stringField(values[pair.dateKey])
		if !ok {
			continue
		}
		subseconds, hasSubseconds := stringField(values[pair.subsecKey])
		if hasSubseconds && decimalDigits(subseconds) {
			date += "." + subseconds
		} else {
			hasSubseconds = false
		}
		offset, hasOffset := stringField(values[pair.offsetKey])
		display, ok := recordedDate(date, offset, hasOffset)
		if !ok {
			continue
		}
		handled[pair.dateKey] = true
		fieldCount := 1
		if hasSubseconds {
			handled[pair.subsecKey] = true
			fieldCount++
		}
		if hasOffset {
			handled[pair.offsetKey] = true
			fieldCount++
		}
		w.lineFields(fieldCount, joinLabel(label, readableKey(pair.dateKey)), display)
	}
}

func (w projectionWriter) gps(label, path string, values map[string]Value, handled map[string]bool) {
	latitude, latOK := numberField(values["Latitude"])
	longitude, lonOK := numberField(values["Longitude"])
	if latOK && lonOK {
		latitudeRef, hasLatitudeRef := stringField(values["LatitudeRef"])
		longitudeRef, hasLongitudeRef := stringField(values["LongitudeRef"])
		if (hasLatitudeRef && !stringEnumKnown(latitudeRef, "N", "S")) || (hasLongitudeRef && !stringEnumKnown(longitudeRef, "E", "W")) {
			for _, key := range []string{"Latitude", "Longitude", "LatitudeRef", "LongitudeRef"} {
				if _, exists := values[key]; exists {
					handled[key] = true
					w.exclude(path+"."+key, "camera position retained; omitted from model input because a coordinate reference is unrecognised")
				}
			}
		} else {
			latitude = applyCoordinateRef(latitude, latitudeRef, "N", "S")
			longitude = applyCoordinateRef(longitude, longitudeRef, "E", "W")
			accuracy, accuracyKey, hasAccuracy := gpsAccuracy(values)
			latitudeDigits := coordinateDigits(accuracy, 111320, hasAccuracy)
			longitudeDigits := coordinateDigits(accuracy, longitudeMetresPerDegree(latitude), hasAccuracy)
			position := formatCoordinate(latitude, latitudeDigits) + " degrees, " + formatCoordinate(longitude, longitudeDigits) + " degrees"
			if hasAccuracy {
				position += " (accuracy " + formatDistance(accuracy) + ")"
			}
			fieldCount := 0
			for _, key := range []string{"Latitude", "Longitude", "LatitudeRef", "LongitudeRef", accuracyKey} {
				if key == "" {
					continue
				}
				if _, exists := values[key]; !exists {
					continue
				}
				handled[key] = true
				fieldCount++
			}
			w.lineFields(fieldCount, joinLabel(label, "Camera position"), position)
		}
	}

	w.gpsAltitude(label, path, values, handled)
	w.gpsMeasurement(label, path, values, handled, "Speed", "SpeedRef", gpsSpeedUnit)
	w.gpsMeasurement(label, path, values, handled, "Track", "TrackRef", gpsDirectionUnit)
	w.gpsMeasurement(label, path, values, handled, "ImgDirection", "ImgDirectionRef", gpsDirectionUnit)
	w.gpsMeasurement(label, path, values, handled, "DestBearing", "DestBearingRef", gpsDirectionUnit)
	w.gpsMeasurement(label, path, values, handled, "DestDistance", "DestDistanceRef", gpsDistanceUnit)
	w.gpsTimestamp(label, path, values, handled)
}

func stringEnumKnown(value string, allowed ...string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (w projectionWriter) gpsAltitude(label, path string, values map[string]Value, handled map[string]bool) {
	altitude, ok := numberField(values["Altitude"])
	if !ok {
		return
	}
	display := significant(altitude, 4) + " m"
	fieldCount := 1
	if reference, exists := integerField(values["AltitudeRef"]); exists {
		switch reference {
		case 0:
			display += " above sea level"
		case 1:
			display += " below sea level"
		default:
			return
		}
		handled["AltitudeRef"] = true
		fieldCount++
	}
	handled["Altitude"] = true
	w.lineFields(fieldCount, joinLabel(label, "Altitude"), display)
}

func (w projectionWriter) gpsMeasurement(label, path string, values map[string]Value, handled map[string]bool, valueKey, referenceKey string, unit func(string) (string, bool)) {
	measurement, ok := numberField(values[valueKey])
	if !ok {
		return
	}
	reference, ok := stringField(values[referenceKey])
	if !ok {
		w.exclude(path+"."+valueKey, "measurement retained; omitted from model input because its unit reference is missing")
		handled[valueKey] = true
		return
	}
	suffix, ok := unit(reference)
	if !ok {
		w.exclude(path+"."+valueKey, "measurement retained; omitted from model input because its unit reference is unrecognised")
		w.exclude(path+"."+referenceKey, "unrecognised unit reference retained; omitted from model input")
		handled[valueKey] = true
		handled[referenceKey] = true
		return
	}
	handled[valueKey] = true
	handled[referenceKey] = true
	w.lineFields(2, joinLabel(label, readableKey(valueKey)), significant(measurement, 4)+suffix)
}

func (w projectionWriter) gpsTimestamp(label, path string, values map[string]Value, handled map[string]bool) {
	date, hasDate := stringField(values["DateStamp"])
	timeValue, hasTime := values["TimeStamp"]
	if !hasDate || !hasTime {
		return
	}
	parts := make([]float64, 3)
	fieldCount := 2
	switch timeValue.Type {
	case ValueArray:
		if len(timeValue.Array) != 3 {
			return
		}
		fieldCount = 4
		for index, part := range timeValue.Array {
			value, ok := numberField(part)
			if !ok {
				return
			}
			parts[index] = value
		}
	case ValueString:
		parsed, err := time.Parse("15:04:05.999999999", stringValue(timeValue.String))
		if err != nil {
			return
		}
		parts[0] = float64(parsed.Hour())
		parts[1] = float64(parsed.Minute())
		parts[2] = float64(parsed.Second()) + float64(parsed.Nanosecond())/1e9
	default:
		return
	}
	parsedDate, err := time.Parse("2006:01:02", date)
	if err != nil || parts[0] < 0 || parts[0] >= 24 || parts[1] < 0 || parts[1] >= 60 || parts[2] < 0 || parts[2] >= 60 {
		return
	}
	seconds := strconv.FormatFloat(parts[2], 'f', -1, 64)
	if parts[2] < 10 {
		seconds = "0" + seconds
	}
	display := fmt.Sprintf("%s at %02.0f:%02.0f:%s UTC", parsedDate.Format("2 January 2006"), parts[0], parts[1], seconds)
	handled["DateStamp"] = true
	handled["TimeStamp"] = true
	w.lineFields(fieldCount, joinLabel(label, "Recorded time"), display)
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func (w projectionWriter) resolution(label, path string, values map[string]Value, handled map[string]bool) {
	unitValue, ok := integerField(values["ResolutionUnit"])
	if !ok {
		return
	}
	unit := ""
	switch unitValue {
	case 2:
		unit = " pixels per inch"
	case 3:
		unit = " pixels per centimetre"
	default:
		return
	}
	for _, key := range []string{"XResolution", "YResolution"} {
		value, exists := numberField(values[key])
		if !exists {
			continue
		}
		handled[key] = true
		w.line(joinLabel(label, readableKey(key)), significant(value, 4)+unit)
	}
}

func (w projectionWriter) knownArrays(label, path string, values map[string]Value, handled map[string]bool) {
	for _, key := range []string{"ISOSpeedRatings", "PhotographicSensitivity"} {
		value, exists := values[key]
		if !exists || value.Type != ValueArray || len(value.Array) == 0 {
			continue
		}
		parts := make([]string, 0, len(value.Array))
		for _, item := range value.Array {
			integer, ok := integerField(item)
			if !ok || integer < 0 {
				parts = nil
				break
			}
			parts = append(parts, strconv.FormatInt(integer, 10))
		}
		if parts != nil {
			handled[key] = true
			w.lineFields(len(value.Array), joinLabel(label, readableKey(key)), strings.Join(parts, ", "))
		}
	}

	if value, exists := values["LensSpecification"]; exists && value.Type == ValueArray && len(value.Array) == 4 {
		parts := make([]float64, 4)
		valid := true
		for index, item := range value.Array {
			parts[index], valid = numberField(item)
			if !valid {
				break
			}
		}
		if valid {
			display := significant(parts[0], 3) + " to " + significant(parts[1], 3) + " mm; maximum aperture f/" + significant(parts[2], 2) + " to f/" + significant(parts[3], 2)
			handled["LensSpecification"] = true
			w.lineFields(4, joinLabel(label, readableKey("LensSpecification")), display)
		}
	}
}

func (w projectionWriter) redundantAPEX(label, path string, values map[string]Value, handled map[string]bool) {
	if _, exposure := values["ExposureTime"]; exposure {
		if _, apex := values["ShutterSpeedValue"]; apex {
			handled["ShutterSpeedValue"] = true
			w.exclude(path+".ShutterSpeedValue", "redundant APEX value retained; readable exposure time rendered from ExposureTime")
		}
	}
	if _, aperture := values["FNumber"]; aperture {
		if _, apex := values["ApertureValue"]; apex {
			handled["ApertureValue"] = true
			w.exclude(path+".ApertureValue", "redundant APEX value retained; readable aperture rendered from FNumber")
		}
	}
}

func (w projectionWriter) excludeOpaque(path string, value Value) {
	switch value.Type {
	case ValueDictionary:
		keys := make([]string, 0, len(value.Dictionary))
		for key := range value.Dictionary {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			w.excludeOpaque(path+"."+key, value.Dictionary[key])
		}
	case ValueArray:
		for index, item := range value.Array {
			w.excludeOpaque(fmt.Sprintf("%s[%d]", path, index), item)
		}
	case ValueData:
		decoded, _ := base64.StdEncoding.DecodeString(stringValue(value.Data))
		w.exclude(path, fmt.Sprintf("binary data (%d bytes)", len(decoded)))
	case ValueNull:
		w.exclude(path, "null value")
	default:
		w.exclude(path, "opaque namespace value retained; omitted from model input because the field meaning is not declared")
	}
}

func (w projectionWriter) line(label, value string) {
	w.lineFields(1, label, value)
}

func (w projectionWriter) lineFields(fieldCount int, label, value string) {
	w.projection.Lines = append(w.projection.Lines, label+": "+value)
	w.projection.RenderedFieldCount += fieldCount
}

func (w projectionWriter) exclude(path, reason string) {
	w.projection.Exclusions = append(w.projection.Exclusions, Exclusion{Path: path, Reason: reason})
}

func (w projectionWriter) excludeUnrecognised(path string) {
	w.exclude(path, "unrecognised field retained; omitted from model input because its meaning or units are not declared")
}

func joinLabel(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + " › " + child
}

func lastPathPart(path string) string {
	if index := strings.LastIndex(path, "."); index >= 0 {
		return path[index+1:]
	}
	return path
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func countFields(record Record) int {
	total := countValueFields(record.Container)
	for _, image := range record.Images {
		total += countValueFields(image.Properties)
	}
	return total
}

func countValueFields(value Value) int {
	switch value.Type {
	case ValueDictionary:
		total := 0
		for _, child := range value.Dictionary {
			total += countValueFields(child)
		}
		return total
	case ValueArray:
		total := 0
		for _, child := range value.Array {
			total += countValueFields(child)
		}
		return total
	default:
		return 1
	}
}
