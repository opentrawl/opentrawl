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

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
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
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	source, artifacts := metadataTestCardFacts(input, provedOriginalSHA256, metadata.Projection, modelBytes)
	request, err := renderPreparedCardRequest(source, artifacts, nil, modelBytes, classifier)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := renderCardInputPrompt(request.Input.Input)
	if err != nil {
		t.Fatal(err)
	}
	if extractions != 1 || request.Image.SHA256 != modelSHA256 || request.Image.Bytes != int64(len(modelBytes)) {
		t.Fatalf("extractions = %d image = %#v", extractions, request.Image)
	}
	if request.Input.Input.GetFullCurrent().GetSha256() != modelSHA256 {
		t.Fatalf("CardInput full-current sha = %q, want %q", request.Input.Input.GetFullCurrent().GetSha256(), modelSHA256)
	}
	for _, want := range []string{
		`"projection_lines"`,
		`Image 1 › EXIF › Exposure time: 1/120 s`,
		`Image 1 › EXIF › Aperture: f/1.8`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered request missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, base64.StdEncoding.EncodeToString([]byte("synthetic binary metadata"))) {
		t.Fatalf("rendered request contains binary metadata:\n%s", prompt)
	}
	if strings.Contains(prompt, "MysteryScalar") {
		t.Fatalf("rendered request contains an unrecognised raw metadata token:\n%s", prompt)
	}
	t.Logf("RAW exact-original metadata input: path=%s proved_sha256=%s", filepath.Base(originalPath), provedOriginalSHA256)
	t.Logf("RAW model-image input: path=%s sha256=%s", filepath.Base(modelPath), modelSHA256)
	t.Logf("RAW ImageIO output before typed decoding:\n%s", rawRecord)
	t.Logf("RAW rendered model request before provider call:\n%s", request.Request.Body())

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
	restartedSource, restartedArtifacts := metadataTestCardFacts(input, provedOriginalSHA256, restartedMetadata.Projection, modelBytes)
	restartedRequest, err := renderPreparedCardRequest(restartedSource, restartedArtifacts, nil, modelBytes, classifier)
	if err != nil {
		t.Fatal(err)
	}
	if restartedRequest.RequestSHA256 != request.RequestSHA256 || restartedRequest.CardRequestID != request.CardRequestID || restartedRequest.Image != request.Image {
		t.Fatalf("restart request changed\nfirst image: %#v\nsecond image: %#v", request.Image, restartedRequest.Image)
	}
}

func metadataTestCardFacts(input classifyInput, originalSHA256 string, projection imagemetadata.Projection, modelBytes []byte) (cardinput.SourceFacts, cardinput.CheckedArtifacts) {
	modelDigest := sha256.Sum256(modelBytes)
	modelSHA256 := hex.EncodeToString(modelDigest[:])
	original := cardinput.ImmutableOriginalFact{ResourceType: "photo", UTI: "public.jpeg", Filename: "exact-original.jpeg", Availability: "local", SizeBytes: 3, SHA256: originalSHA256}
	metadata := cardinput.MetadataFact{RecordSHA256: metadataTestDigest("metadata-record"), ProjectionSHA256: metadataTestDigest(strings.Join(projection.Lines, "\n")), ProjectionLines: projection.Lines}
	fullCurrent := cardinput.FullCurrentFact{Role: "full_current", MediaType: "public.jpeg", Orientation: 1, PixelWidth: input.Width, PixelHeight: input.Height, SizeBytes: int64(len(modelBytes)), SHA256: modelSHA256}
	source := cardinput.SourceFacts{AssetID: "asset:metadata-request", SourceID: "source:synthetic", CaptureTime: input.CreationDate, Timezone: &input.TimezoneName, MediaType: input.MediaType, PixelWidth: input.Width, PixelHeight: input.Height, ImmutableOriginal: original, Metadata: metadata, FullCurrent: fullCurrent}
	artifacts := cardinput.CheckedArtifacts{ImmutableOriginal: cardinput.CheckedImmutableOriginal{Fact: original, ResourceID: "resource:metadata-original"}, Metadata: cardinput.CheckedMetadata{Fact: metadata, RecordID: "image_metadata:" + metadata.RecordSHA256, ProjectionID: "image_metadata_projection:" + metadata.ProjectionSHA256}, FullCurrent: cardinput.CheckedFullCurrent{Fact: fullCurrent, ProofSHA256: metadataTestDigest("current-still-proof")}}
	return source, artifacts
}

func metadataTestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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
