package imagemetadata

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStorePreservesTypedMultiIndexMetadataAndReusesCheckedArtifacts(t *testing.T) {
	source := filepath.Join(t.TempDir(), "synthetic-original.heic")
	bytes := []byte("synthetic exact original bytes")
	if err := os.WriteFile(source, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	provedSHA256 := hex.EncodeToString(digest[:])
	rawRecord, err := json.Marshal(syntheticRawRecord())
	if err != nil {
		t.Fatal(err)
	}
	extractions := 0
	root := filepath.Join(t.TempDir(), "metadata-cache")
	store, err := NewStore(root, func(_ context.Context, path string) ([]byte, error) {
		extractions++
		if path != source {
			t.Fatalf("extract path = %q, want %q", path, source)
		}
		return rawRecord, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.Load(context.Background(), source, provedSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheHit || extractions != 1 {
		t.Fatalf("first cache hit = %t, extractions = %d", first.CacheHit, extractions)
	}
	if first.Proof.FieldCount != first.Proof.RenderedCount+first.Proof.ExclusionCount {
		t.Fatalf("proof = %#v", first.Proof)
	}
	projection := first.Projection.Text()
	for _, want := range []string{
		"Container › File size: 18446744073709551615 bytes",
		"Container › RecordedDate: 10 July 2026 at 12:34:56 +02:00",
		"Image 1 › EXIF › Original capture time: 10 July 2026 at 12:34:56 +02:00",
		"Image 1 › EXIF › Exposure time: 1/120 s",
		"Image 1 › EXIF › Aperture: f/1.8",
		"Image 1 › EXIF › Focal length: 6.86 mm",
		"Image 1 › EXIF › ISO: 64",
		"Image 1 › GPS › Camera position: 52.3676 degrees, 4.904 degrees (accuracy 8.2 m)",
		"Image 1 › IPTC › Caption: Synthetic caption",
		"Image 2 › EXIF › Exposure time: 1.3 s",
		"Image 2 › EXIF › Flash: fired; red-eye reduction",
		"Image 2 › EXIF › Colour space: sRGB",
		"Image 2 › EXIF › Exposure mode: manual",
		"Image 2 › TIFF › Orientation: rotated 90 degrees clockwise",
	} {
		if !strings.Contains(projection, want) {
			t.Fatalf("projection missing %q:\n%s", want, projection)
		}
	}
	if strings.Contains(projection, base64.StdEncoding.EncodeToString([]byte("private binary payload"))) {
		t.Fatalf("projection contains binary payload:\n%s", projection)
	}
	if len(first.Projection.Exclusions) != 8 {
		t.Fatalf("exclusions = %#v, want binary, null, redundant APEX, opaque maker values, and unrecognised scalars", first.Projection.Exclusions)
	}
	for _, want := range []string{
		"binary data",
		"null value",
		"redundant APEX value retained",
		"opaque namespace value retained",
		"unrecognised field retained",
	} {
		found := false
		for _, exclusion := range first.Projection.Exclusions {
			if strings.Contains(exclusion.Reason, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("exclusions missing %q: %#v", want, first.Projection.Exclusions)
		}
	}

	recordBytes, err := canonicalJSON(first.Record)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Record
	if err := json.Unmarshal(recordBytes, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Record, roundTrip) {
		t.Fatalf("typed round trip changed\nfirst: %#v\nsecond: %#v", first.Record, roundTrip)
	}
	t.Logf("RAW typed record output:\n%s", recordBytes)
	projectionBytes, err := canonicalJSON(first.Projection)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW readable projection output:\n%s", projectionBytes)

	restarted, err := NewStore(root, func(context.Context, string) ([]byte, error) {
		t.Fatal("checked restart cache reached extractor")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Load(context.Background(), source, provedSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit || !reflect.DeepEqual(first.Record, second.Record) || !reflect.DeepEqual(first.Projection, second.Projection) {
		t.Fatalf("restart artifacts changed: first=%#v second=%#v", first.Proof, second.Proof)
	}
	for _, path := range []string{
		root,
		filepath.Join(root, provedSHA256),
		filepath.Join(root, provedSHA256, ExtractorVersion),
	} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %q mode = %v, err = %v", filepath.Base(path), infoMode(info), err)
		}
	}
	for _, name := range []string{recordFilename, projectionFilename, proofFilename} {
		path := filepath.Join(root, provedSHA256, ExtractorVersion, name)
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact %q mode = %v, err = %v", name, infoMode(info), err)
		}
	}
}

func TestProjectRendersKnownIntegerUnitsAndEnumsWithoutRawTokens(t *testing.T) {
	record := Record{
		ExtractorVersion: ExtractorVersion,
		OriginalSHA256:   strings.Repeat("a", 64),
		Container:        dictionary(map[string]Value{}),
		Images: []Image{{
			Index: 0,
			Properties: dictionary(map[string]Value{
				"{Exif}": dictionary(map[string]Value{
					"FocalLength":          unsigned("7"),
					"FNumber":              unsigned("2"),
					"ExposureProgram":      unsigned("3"),
					"MeteringMode":         unsigned("5"),
					"LightSource":          unsigned("10"),
					"SensingMethod":        unsigned("2"),
					"SceneCaptureType":     unsigned("1"),
					"SubjectDistanceRange": unsigned("3"),
					"UnknownEnum":          unsigned("42"),
				}),
				"{GPS}": dictionary(map[string]Value{
					"Altitude":          unsigned("12"),
					"AltitudeRef":       unsigned("1"),
					"Speed":             unsigned("10"),
					"SpeedRef":          stringMetadata("K"),
					"ImgDirection":      unsigned("123"),
					"ImgDirectionRef":   stringMetadata("T"),
					"Status":            stringMetadata("A"),
					"MeasureMode":       stringMetadata("3"),
					"GPSDifferential":   unsigned("1"),
					"DateStamp":         stringMetadata("2026:07:11"),
					"TimeStamp":         stringMetadata("03:15:20.5"),
					"UnknownGPSMeasure": unsigned("99"),
				}),
			}),
		}},
	}

	projection := Project(record)
	text := projection.Text()
	for _, want := range []string{
		"Image 1 › EXIF › Focal length: 7 mm",
		"Image 1 › EXIF › Aperture: f/2",
		"Image 1 › EXIF › Exposure program: aperture priority",
		"Image 1 › EXIF › Metering mode: pattern",
		"Image 1 › EXIF › Light source: cloudy",
		"Image 1 › EXIF › Sensor: one-chip colour area sensor",
		"Image 1 › EXIF › Scene type: landscape",
		"Image 1 › EXIF › Subject distance: distant view",
		"Image 1 › GPS › Altitude: 12 m below sea level",
		"Image 1 › GPS › Speed: 10 km/h",
		"Image 1 › GPS › Camera direction: 123° true",
		"Image 1 › GPS › Position status: measurement active",
		"Image 1 › GPS › Positioning mode: 3D positioning",
		"Image 1 › GPS › Differential correction: differential correction applied",
		"Image 1 › GPS › Recorded time: 11 July 2026 at 03:15:20.5 UTC",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("projection missing %q:\n%s", want, text)
		}
	}
	for _, raw := range []string{"UnknownEnum: 42", "UnknownGPSMeasure: 99"} {
		if strings.Contains(text, raw) {
			t.Fatalf("projection exposed unrecognised raw token %q:\n%s", raw, text)
		}
	}
	for _, path := range []string{"image[0].{Exif}.UnknownEnum", "image[0].{GPS}.UnknownGPSMeasure"} {
		found := false
		for _, exclusion := range projection.Exclusions {
			if exclusion.Path == path && strings.Contains(exclusion.Reason, "unrecognised field retained") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("projection exclusions missing %q: %#v", path, projection.Exclusions)
		}
	}
}

func TestStoreRejectsOriginalWhoseBytesDoNotMatchResolverProof(t *testing.T) {
	source := filepath.Join(t.TempDir(), "changed.heic")
	if err := os.WriteFile(source, []byte("changed bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(t.TempDir(), func(context.Context, string) ([]byte, error) {
		t.Fatal("mismatched original reached ImageIO")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background(), source, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "exact original SHA-256 mismatch") {
		t.Fatalf("Load error = %v, want digest mismatch", err)
	}
}

func TestStoreReextractsWhenCachedProjectionFailsItsDigest(t *testing.T) {
	source := filepath.Join(t.TempDir(), "synthetic-original.heic")
	bytes := []byte("synthetic original for corrupt cache")
	if err := os.WriteFile(source, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	provedSHA256 := hex.EncodeToString(digest[:])
	rawRecord, err := json.Marshal(syntheticRawRecord())
	if err != nil {
		t.Fatal(err)
	}
	extractions := 0
	extractor := func(context.Context, string) ([]byte, error) {
		extractions++
		return rawRecord, nil
	}
	root := t.TempDir()
	store, err := NewStore(root, extractor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), source, provedSHA256); err != nil {
		t.Fatal(err)
	}
	projectionPath := filepath.Join(root, provedSHA256, ExtractorVersion, projectionFilename)
	if err := os.WriteFile(projectionPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(root, extractor)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := restarted.Load(context.Background(), source, provedSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.CacheHit || extractions != 2 {
		t.Fatalf("cache hit = %t, extractions = %d", artifacts.CacheHit, extractions)
	}
}

func TestStoreRejectsUnknownTypedRecordFieldsInsteadOfDroppingThem(t *testing.T) {
	source := filepath.Join(t.TempDir(), "synthetic-original.heic")
	bytes := []byte("synthetic original for unknown metadata field")
	if err := os.WriteFile(source, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	store, err := NewStore(t.TempDir(), func(context.Context, string) ([]byte, error) {
		return []byte(`{"container":{"type":"dictionary","dictionary":{}},"images":[],"unexpected":"would be dropped"}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background(), source, hex.EncodeToString(digest[:]))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load error = %v, want unknown field rejection", err)
	}
}

func syntheticRawRecord() Record {
	return Record{
		Container: dictionary(map[string]Value{
			"FileSize":     unsigned("18446744073709551615"),
			"HasThumbnail": booleanMetadata(true),
			"SignedOffset": signed("-2"),
			"RecordedDate": dateMetadata("2026-07-10T12:34:56+02:00"),
		}),
		Images: []Image{
			{
				Index: 0,
				Properties: dictionary(map[string]Value{
					"{Exif}": dictionary(map[string]Value{
						"ExposureTime":       decimal("0.008333333333333333"),
						"ShutterSpeedValue":  decimal("6.906890595608519"),
						"FNumber":            decimal("1.7999999523162842"),
						"ApertureValue":      decimal("1.6959938131099002"),
						"FocalLength":        decimal("6.859999999999999"),
						"ISOSpeedRatings":    array(unsigned("64")),
						"DateTimeOriginal":   stringMetadata("2026:07:10 12:34:56"),
						"OffsetTimeOriginal": stringMetadata("+02:00"),
						"MysteryScalar":      decimal("1.234567890123456789"),
						"MakerBlob":          dataMetadata([]byte("private binary payload")),
						"MissingValue":       {Type: ValueNull},
					}),
					"{GPS}": dictionary(map[string]Value{
						"Latitude":             decimal("52.367612345678"),
						"LatitudeRef":          stringMetadata("N"),
						"Longitude":            decimal("4.904112345678"),
						"LongitudeRef":         stringMetadata("E"),
						"GPSHPositioningError": decimal("8.25"),
					}),
					"{IPTC}": dictionary(map[string]Value{
						"Caption/Abstract": stringMetadata("Synthetic caption"),
					}),
					"{MakerApple}": dictionary(map[string]Value{
						"VendorCode": unsigned("7"),
						"VendorBlob": dataMetadata([]byte("opaque maker data")),
					}),
				}),
			},
			{
				Index: 1,
				Properties: dictionary(map[string]Value{
					"{TIFF}": dictionary(map[string]Value{
						"Orientation": unsigned("6"),
					}),
					"{Exif}": dictionary(map[string]Value{
						"ExposureTime": decimal("1.3"),
						"Flash":        unsigned("65"),
						"ColorSpace":   unsigned("1"),
						"ExposureMode": unsigned("1"),
					}),
				}),
			},
		},
	}
}

func dictionary(values map[string]Value) Value {
	return Value{Type: ValueDictionary, Dictionary: values}
}

func array(values ...Value) Value {
	return Value{Type: ValueArray, Array: values}
}

func stringMetadata(value string) Value {
	return Value{Type: ValueString, String: &value}
}

func unsigned(value string) Value {
	return Value{Type: ValueUnsigned, Unsigned: &value}
}

func signed(value string) Value {
	return Value{Type: ValueSigned, Signed: &value}
}

func booleanMetadata(value bool) Value {
	return Value{Type: ValueBoolean, Boolean: &value}
}

func dateMetadata(value string) Value {
	return Value{Type: ValueDate, Date: &value}
}

func decimal(value string) Value {
	return Value{Type: ValueDecimal, Decimal: &value}
}

func dataMetadata(value []byte) Value {
	encoded := base64.StdEncoding.EncodeToString(value)
	return Value{Type: ValueData, Data: &encoded}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}
