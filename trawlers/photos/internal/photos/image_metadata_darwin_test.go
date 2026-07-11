//go:build darwin

package photos

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageMetadataRecordReadsContainerAndEveryImageIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "two-frame.gif")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.SetColorIndex(0, 0, 1)
	if err := gif.EncodeAll(file, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{0, 5}}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := ImageMetadataRecord(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Container map[string]any `json:"container"`
		Images    []struct {
			Index      int            `json:"index"`
			Properties map[string]any `json:"properties"`
		} `json:"images"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode raw record: %v\n%s", err, raw)
	}
	if record.Container["type"] != "dictionary" {
		t.Fatalf("container = %#v", record.Container)
	}
	if len(record.Images) != 2 || record.Images[0].Index != 0 || record.Images[1].Index != 1 {
		t.Fatalf("images = %#v", record.Images)
	}
	for _, imageRecord := range record.Images {
		if imageRecord.Properties["type"] != "dictionary" {
			t.Fatalf("image %d properties = %#v", imageRecord.Index, imageRecord.Properties)
		}
	}
	t.Logf("RAW ImageIO record output:\n%s", raw)
}

func TestImageMetadataRecordReadsNestedImageIOFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested-imageio-fixture.jpeg")
	writeImageMetadataFixture(t, path)
	raw, err := ImageMetadataRecord(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"{Exif}"`, `"{GPS}"`, `"{TIFF}"`, `"{IPTC}"`,
		`"DateTimeOriginal"`, `"OffsetTimeOriginal"`, `"HPositioningError"`,
		`"Synthetic caption"`, `"type":"array"`, `"type":"decimal"`,
		`"type":"signed_integer"`, `"type":"string"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("raw ImageIO record missing %s:\n%s", want, raw)
		}
	}
	t.Logf("RAW nested ImageIO record output:\n%s", raw)
}

func TestImageMetadataTypedBridgeFixturePreservesEverySupportedValueType(t *testing.T) {
	raw := typedMetadataFixture(t)
	for _, want := range []string{
		`"type":"string"`, `"type":"boolean"`, `"type":"signed_integer"`,
		`"type":"unsigned_integer"`, `"type":"decimal"`, `"type":"date"`,
		`"type":"data"`, `"type":"array"`, `"type":"dictionary"`, `"type":"null"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("typed bridge fixture missing %s:\n%s", want, raw)
		}
	}
	t.Logf("RAW typed bridge fixture output:\n%s", raw)
}

func writeImageMetadataFixture(t *testing.T, path string) {
	t.Helper()
	if err := writeImageMetadataFixtureForTest(path); err != nil {
		t.Fatal(err)
	}
}

func typedMetadataFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := imageMetadataTypedFixtureForTest()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
