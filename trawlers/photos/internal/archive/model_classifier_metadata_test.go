package archive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
)

func TestBuildRequestSeparatesOriginalMetadataFromModelImageIdentity(t *testing.T) {
	originalPath := filepath.Join(t.TempDir(), "exact-original.jpeg")
	originalBytes := syntheticImageBytes(t)
	if err := os.WriteFile(originalPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(t.TempDir(), "current-still.jpeg")
	modelBytes := syntheticAlternateImageBytes(t)
	if err := os.WriteFile(modelPath, modelBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(originalBytes)
	provedOriginalSHA256 := hex.EncodeToString(originalDigest[:])
	modelDigest := sha256.Sum256(modelBytes)
	modelSHA256 := hex.EncodeToString(modelDigest[:])
	if provedOriginalSHA256 == modelSHA256 {
		t.Fatal("fixture did not make distinct original and model-image identities")
	}
	rawRecord := archiveSyntheticMetadataRecord(t)
	cacheRoot := filepath.Join(t.TempDir(), "image-metadata")
	extractions := 0
	metadataStore, err := imagemetadata.NewStore(cacheRoot, func(_ context.Context, path string) ([]byte, error) {
		extractions++
		if path != originalPath {
			t.Fatalf("ImageIO input = %q, want %q", path, originalPath)
		}
		return rawRecord, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	classifier := modelClassifier{}
	input := classifyInput{
		CreationDate: "2026-07-10T10:00:00Z",
		TimezoneName: "Europe/Amsterdam",
		MediaType:    "image",
		Width:        4032,
		Height:       3024,
	}
	metadata, err := metadataStore.Load(context.Background(), originalPath, provedOriginalSHA256)
	if err != nil {
		t.Fatal(err)
	}
	request, meta, err := classifier.buildRequest(input, modelPath, metadata.Projection)
	if err != nil {
		t.Fatal(err)
	}
	if extractions != 1 || meta.SHA256 != modelSHA256 || meta.Bytes != int64(len(modelBytes)) {
		t.Fatalf("extractions = %d meta = %#v", extractions, meta)
	}
	if len(request.Images) != 1 || string(request.Images[0].Data) != string(modelBytes) {
		t.Fatalf("request image = %#v", request.Images)
	}
	for _, want := range []string{
		`"exact_original_metadata": [`,
		`Image 1 › EXIF › Exposure time: 1/120 s`,
		`Image 1 › EXIF › Aperture: f/1.8`,
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("rendered request missing %q:\n%s", want, request.Prompt)
		}
	}
	if strings.Contains(request.Prompt, base64.StdEncoding.EncodeToString([]byte("synthetic binary metadata"))) {
		t.Fatalf("rendered request contains binary metadata:\n%s", request.Prompt)
	}
	if strings.Contains(request.Prompt, "MysteryScalar") {
		t.Fatalf("rendered request contains an unrecognised raw metadata token:\n%s", request.Prompt)
	}
	t.Logf("RAW exact-original metadata input: path=%s proved_sha256=%s", filepath.Base(originalPath), provedOriginalSHA256)
	t.Logf("RAW model-image input: path=%s sha256=%s", filepath.Base(modelPath), modelSHA256)
	t.Logf("RAW ImageIO output before typed decoding:\n%s", rawRecord)
	t.Logf("RAW rendered model request before provider call:\n%s", request.Prompt)

	restartedStore, err := imagemetadata.NewStore(cacheRoot, func(context.Context, string) ([]byte, error) {
		t.Fatal("checked metadata restart reached ImageIO")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	restartedMetadata, err := restartedStore.Load(context.Background(), originalPath, provedOriginalSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !restartedMetadata.CacheHit {
		t.Fatal("restart metadata was not a checked cache hit")
	}
	restartedRequest, restartedMeta, err := classifier.buildRequest(input, modelPath, restartedMetadata.Projection)
	if err != nil {
		t.Fatal(err)
	}
	if restartedRequest.Prompt != request.Prompt || restartedMeta != meta {
		t.Fatalf("restart request changed\nfirst meta: %#v\nsecond meta: %#v", meta, restartedMeta)
	}
}

func archiveSyntheticMetadataRecord(t *testing.T) []byte {
	t.Helper()
	decimal := func(value string) imagemetadata.Value {
		return imagemetadata.Value{Type: imagemetadata.ValueDecimal, Decimal: &value}
	}
	data := base64.StdEncoding.EncodeToString([]byte("synthetic binary metadata"))
	record := imagemetadata.Record{
		Container: imagemetadata.Value{Type: imagemetadata.ValueDictionary, Dictionary: map[string]imagemetadata.Value{}},
		Images: []imagemetadata.Image{{
			Index: 0,
			Properties: imagemetadata.Value{Type: imagemetadata.ValueDictionary, Dictionary: map[string]imagemetadata.Value{
				"{Exif}": {Type: imagemetadata.ValueDictionary, Dictionary: map[string]imagemetadata.Value{
					"ExposureTime":  decimal("0.008333333333333333"),
					"FNumber":       decimal("1.7999999523162842"),
					"MysteryScalar": decimal("1.234567890123456789"),
					"MakerBlob":     {Type: imagemetadata.ValueData, Data: &data},
				}},
			}},
		}},
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
