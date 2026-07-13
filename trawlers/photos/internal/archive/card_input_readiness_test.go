package archive

import (
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestValidateCardInputLiveReadinessBindsBothMediaBoundaries(t *testing.T) {
	input := classifyInput{
		AssetID:          "asset:synthetic",
		SourceLibraryID:  "source:synthetic",
		LocalIdentifier:  "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		SourceState:      sourceStateCurrent,
		MediaType:        "image",
		CreationDate:     "2026-07-14T12:00:00Z",
		ModificationDate: "2026-07-14T12:00:01Z",
		Width:            4032,
		Height:           3024,
		Resources: []classifyResource{{
			ResourceType: "photo", OriginalFilename: "synthetic.heic", UTI: "public.heic",
		}},
	}
	readiness := photos.AssetReadiness{
		LocalIdentifier:  "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001",
		AssetUUID:        "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		MediaType:        "image",
		CreationDate:     "2026-07-14T12:00:00Z",
		ModificationDate: "2026-07-14T12:00:01Z",
		PixelWidth:       4032,
		PixelHeight:      3024,
		OriginalFilename: "synthetic.heic",
		OriginalUTI:      "public.heic",
	}
	if err := validateCardInputLiveReadiness(input, readiness); err != nil {
		t.Fatal(err)
	}
	readiness.ModificationDate = "2026-07-14T12:00:02Z"
	if err := validateCardInputLiveReadiness(input, readiness); err == nil {
		t.Fatal("changed current-still freshness passed readiness")
	}
}
