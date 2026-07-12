package archive

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func withSyntheticCurrentStill(t *testing.T) {
	t.Helper()
	old := newCurrentStillResolver
	mediaPath := filepath.Join(t.TempDir(), "synthetic-current-still.jpeg")
	newCurrentStillResolver = func(_ string, _ photos.CurrentStillExporter) (currentStillResolver, error) {
		return syntheticCurrentStillResolver{path: mediaPath}, nil
	}
	t.Cleanup(func() { newCurrentStillResolver = old })
}

type syntheticCurrentStillResolver struct{ path string }

func (r syntheticCurrentStillResolver) Resolve(_ context.Context, _ photos.CurrentStillRequest) (photos.CurrentStillResolution, error) {
	data := []byte("synthetic full-resolution current-still fixture")
	if err := os.WriteFile(r.path, data, 0o600); err != nil {
		return photos.CurrentStillResolution{}, err
	}
	digest := sha256.Sum256(data)
	return photos.CurrentStillResolution{Path: r.path, Source: "synthetic_current_still", Fact: photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 4032, PixelHeight: 3024, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", digest)}}, nil
}

func TestCurrentStillMIMEType(t *testing.T) {
	for _, test := range []struct{ mediaType, want string }{{"public.heic", "image/heic"}, {"public.heif", "image/heif"}, {"public.jpeg", "image/jpeg"}, {"public.png", "image/png"}} {
		if got, err := currentStillMIMEType(test.mediaType); err != nil || got != test.want {
			t.Fatalf("currentStillMIMEType(%q) = %q, %v", test.mediaType, got, err)
		}
	}
	if _, err := currentStillMIMEType(""); !errors.Is(err, errUnknownCurrentStillMIMEType) {
		t.Fatalf("unknown type error = %v", err)
	}
}

func TestCurrentStillRequestCanonicalizesFloatDerivedFraction(t *testing.T) {
	input := classifyInput{
		SourceLibraryID:  "source-library:synthetic",
		LocalIdentifier:  "synthetic-asset",
		ModificationDate: "2023-03-24T05:18:11.797890305Z",
	}
	request, err := input.currentStillRequest()
	if err != nil {
		t.Fatal(err)
	}
	if request.Modification.UnixSeconds != 1679635091 || request.Modification.Microseconds != 797890 {
		t.Fatalf("request modification = %#v", request.Modification)
	}
}
