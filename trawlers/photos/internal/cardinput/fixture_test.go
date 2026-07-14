package cardinput

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
)

func fixtureInput() (SourceFacts, CheckedArtifacts, []place.EvidenceRecord) {
	timezone := "Europe/Amsterdam"
	accuracy := 8.5
	focalLength := 24.0
	focalLength35 := 36.0
	aperture := 2.8
	shutterSpeed := 0.004
	iso := int64(125)
	original := ImmutableOriginalFact{ResourceType: "photo", UTI: "public.heic", Filename: "synthetic.heic", Availability: "local", SizeBytes: 12345, SHA256: digest("original")}
	metadata := MetadataFact{RecordSHA256: digest("metadata-record"), ProjectionSHA256: digest("metadata-projection"), ProjectionLines: []string{"Camera: Example Camera", "Lens: Example 24mm"}}
	current := FullCurrentFact{Role: "full_current", MediaType: "public.jpeg", Orientation: 6, PixelWidth: 4000, PixelHeight: 3000, SizeBytes: 98765, SHA256: digest("full-current")}
	source := SourceFacts{
		AssetID:           "asset:synthetic-one",
		SourceID:          "source:synthetic-library",
		CaptureTime:       "2026-07-13T10:30:00Z",
		Timezone:          &timezone,
		MediaType:         "image",
		MediaSubtypes:     []string{"photo", "hdr"},
		PixelWidth:        4032,
		PixelHeight:       3024,
		DurationSeconds:   0,
		ImmutableOriginal: original,
		Favorite:          true,
		Hidden:            false,
		BurstMember:       true,
		Albums:            []AlbumFact{{Title: "Synthetic favourites", Kind: "regular"}, {Title: "Synthetic trip", Kind: "smart"}},
		Location:          &LocationFact{Latitude: 52.1234, Longitude: 4.5678, HorizontalAccuracyMeters: &accuracy},
		KnownPlace:        &KnownPlaceFact{Name: "Example Studio", Relationship: "work"},
		Camera:            &CameraFact{Make: "Example", Model: "Camera One", LensModel: "Example 24mm", FocalLengthMM: &focalLength, FocalLength35MM: &focalLength35, Aperture: &aperture, ShutterSpeed: &shutterSpeed, ISO: &iso},
		Metadata:          metadata,
		FullCurrent:       current,
		RequiredPlaceOperations: []string{
			"synthetic-reverse",
			"synthetic-nearby",
		},
	}
	artifacts := CheckedArtifacts{
		ImmutableOriginal: CheckedImmutableOriginal{Fact: original, ResourceID: "resource:synthetic-one"},
		Metadata:          CheckedMetadata{Fact: MetadataFact{RecordSHA256: metadata.RecordSHA256, ProjectionSHA256: metadata.ProjectionSHA256, ProjectionLines: slices.Clone(metadata.ProjectionLines)}, RecordID: "metadata-record:synthetic-one", ProjectionID: "metadata-projection:synthetic-one"},
		FullCurrent:       CheckedFullCurrent{Fact: current, ProofSHA256: digest("full-current-proof")},
	}
	records := []place.EvidenceRecord{
		{
			Input:                evidenceInput(source),
			ProviderIdentity:     "synthetic-apple",
			Operation:            "synthetic-reverse",
			CoordinateVariant:    "source-coordinate",
			ParserVersion:        "synthetic-parser-v1",
			PreAuthRequestFile:   "/private/synthetic/reverse-request.raw",
			PreAuthRequestSHA256: digest("reverse-request"),
			RawResponseFile:      "/private/synthetic/reverse-response.raw",
			RawResponseSHA256:    digest("reverse-response"),
			RawHeadersFile:       "/private/synthetic/reverse-headers.raw",
			RawHeadersSHA256:     digest("reverse-headers"),
			HTTPStatus:           200,
			Address:              fixtureAddress("reverse"),
			CompletionState:      "complete",
			CacheIdentity:        "cache:reverse",
			Cached:               true,
			RecordDir:            "/private/synthetic/reverse",
			CredentialReference:  "SYNTHETIC_PROVIDER_KEY",
			StartedAt:            "2026-07-13T10:31:00Z",
			CompletedAt:          "2026-07-13T10:31:01Z",
			DurationMilliseconds: 1000,
		},
		{
			Input:                evidenceInput(source),
			ProviderIdentity:     "synthetic-osm",
			Operation:            "synthetic-nearby",
			CoordinateVariant:    "source-coordinate",
			ParserVersion:        "synthetic-parser-v1",
			PreAuthRequestFile:   "/private/synthetic/nearby-request.raw",
			PreAuthRequestSHA256: digest("nearby-request"),
			RawResponseFile:      "/private/synthetic/nearby-response.raw",
			RawResponseSHA256:    digest("nearby-response"),
			RawHeadersFile:       "/private/synthetic/nearby-headers.raw",
			RawHeadersSHA256:     digest("nearby-headers"),
			HTTPStatus:           200,
			Candidates: []place.EvidenceCandidate{
				{ProviderIndex: 0, ProviderID: "place-z", Name: "Zulu synthetic venue", Categories: []string{"museum", "gallery"}, Coordinate: &place.Coordinate{Latitude: 52.1235, Longitude: 4.5679}, DistanceM: 12.5, Address: fixtureAddress("candidate-z"), Source: "synthetic-osm"},
				{ProviderIndex: 1, ProviderID: "place-a", Name: "Alpha synthetic venue", Categories: []string{"park", "garden"}, Coordinate: &place.Coordinate{Latitude: 52.1236, Longitude: 4.5680}, DistanceM: 24.75, Address: fixtureAddress("candidate-a"), Source: "synthetic-osm"},
			},
			CompletionState:      "complete",
			CacheIdentity:        "cache:nearby",
			RecordDir:            "/private/synthetic/nearby",
			CredentialReference:  "SYNTHETIC_PROVIDER_KEY",
			StartedAt:            "2026-07-13T10:31:02Z",
			CompletedAt:          "2026-07-13T10:31:03Z",
			DurationMilliseconds: 1000,
		},
	}
	return source, artifacts, records
}

func evidenceInput(source SourceFacts) place.Input {
	accuracy := -1.0
	if source.Location.HorizontalAccuracyMeters != nil {
		accuracy = *source.Location.HorizontalAccuracyMeters
	}
	return place.Input{AssetID: source.AssetID, ImagePath: "/private/synthetic/current.jpeg", TakenAt: source.CaptureTime, Location: place.Coordinate{Latitude: source.Location.Latitude, Longitude: source.Location.Longitude}, AccuracyMeters: accuracy}
}

func fixtureAddress(prefix string) *place.Address {
	return &place.Address{Name: prefix + " name", Thoroughfare: "Example Street", SubThoroughfare: "42", Locality: "Example Town", SubLocality: "Example Quarter", AdministrativeArea: "Example Province", SubAdministrativeArea: "Example District", PostalCode: "1234 AB", Country: "Exampleland", ISOCountryCode: "EX", TimeZone: "Europe/Amsterdam", AreasOfInterest: []string{"Example square", "Example canal"}, Formatted: "42 Example Street, Example Town", Source: prefix + " source"}
}

func expectedInput(source SourceFacts, records []place.EvidenceRecord) *cardwire.CardInput {
	return &cardwire.CardInput{
		SchemaVersion:     SchemaVersion,
		CaptureTime:       source.CaptureTime,
		Timezone:          source.Timezone,
		MediaType:         source.MediaType,
		MediaSubtypes:     []string{"photo", "hdr"},
		PixelWidth:        4032,
		PixelHeight:       3024,
		DurationSeconds:   0,
		ImmutableOriginal: &cardwire.ImmutableOriginal{ResourceType: "photo", Uti: "public.heic", Filename: "synthetic.heic", Availability: "local", SizeBytes: 12345, Sha256: digest("original")},
		Favorite:          true,
		Hidden:            false,
		BurstMember:       true,
		Albums:            []*cardwire.Album{{Title: "Synthetic favourites", Kind: "regular"}, {Title: "Synthetic trip", Kind: "smart"}},
		Location:          &cardwire.Location{Latitude: 52.1234, Longitude: 4.5678, HorizontalAccuracyMeters: source.Location.HorizontalAccuracyMeters},
		KnownPlace:        &cardwire.KnownPlace{Name: "Example Studio", Relationship: "work"},
		Camera:            &cardwire.Camera{Make: "Example", Model: "Camera One", LensModel: "Example 24mm", FocalLengthMm: source.Camera.FocalLengthMM, FocalLength_35Mm: source.Camera.FocalLength35MM, Aperture: source.Camera.Aperture, ShutterSpeed: source.Camera.ShutterSpeed, Iso: source.Camera.ISO},
		Metadata:          &cardwire.Metadata{RecordSha256: digest("metadata-record"), ProjectionSha256: digest("metadata-projection"), ProjectionLines: []string{"Camera: Example Camera", "Lens: Example 24mm"}},
		FullCurrent:       &cardwire.FullCurrent{Role: "full_current", MediaType: "public.jpeg", Orientation: 6, PixelWidth: 4000, PixelHeight: 3000, SizeBytes: 98765, Sha256: digest("full-current")},
		Places: []*cardwire.PlaceProjection{
			{ProviderIdentity: "synthetic-apple", Operation: "synthetic-reverse", CoordinateVariant: "source-coordinate", ParserVersion: "synthetic-parser-v1", PreAuthRequestSha256: digest("reverse-request"), RawResponseSha256: digest("reverse-response"), Address: expectedAddress("reverse")},
			{ProviderIdentity: "synthetic-osm", Operation: "synthetic-nearby", CoordinateVariant: "source-coordinate", ParserVersion: "synthetic-parser-v1", PreAuthRequestSha256: digest("nearby-request"), RawResponseSha256: digest("nearby-response"), Candidates: []*cardwire.PlaceCandidate{
				{ProviderIndex: 0, ProviderId: "place-z", Name: "Zulu synthetic venue", Categories: []string{"museum", "gallery"}, Coordinate: &cardwire.Coordinate{Latitude: 52.1235, Longitude: 4.5679}, DistanceMeters: 12.5, Address: expectedAddress("candidate-z"), Source: "synthetic-osm", CandidateId: "place_2_candidate_1"},
				{ProviderIndex: 1, ProviderId: "place-a", Name: "Alpha synthetic venue", Categories: []string{"park", "garden"}, Coordinate: &cardwire.Coordinate{Latitude: 52.1236, Longitude: 4.5680}, DistanceMeters: 24.75, Address: expectedAddress("candidate-a"), Source: "synthetic-osm", CandidateId: "place_2_candidate_2"},
			}},
		},
	}
}

func expectedAddress(prefix string) *cardwire.Address {
	return &cardwire.Address{Name: prefix + " name", Thoroughfare: "Example Street", SubThoroughfare: "42", Locality: "Example Town", SubLocality: "Example Quarter", AdministrativeArea: "Example Province", SubAdministrativeArea: "Example District", PostalCode: "1234 AB", Country: "Exampleland", IsoCountryCode: "EX", TimeZone: "Europe/Amsterdam", AreasOfInterest: []string{"Example square", "Example canal"}, Formatted: "42 Example Street, Example Town", Source: prefix + " source"}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func requireBuild(t *testing.T, source SourceFacts, artifacts CheckedArtifacts, records []place.EvidenceRecord) Result {
	t.Helper()
	result, err := Build(source, artifacts, records)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
