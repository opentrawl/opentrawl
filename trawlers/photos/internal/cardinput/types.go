package cardinput

import (
	"errors"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
)

const (
	SchemaVersion                   uint32 = 1
	MaxModelCandidatesPerProjection        = 20
)

var (
	ErrInvalidSource      = errors.New("invalid source facts")
	ErrIncompleteArtifact = errors.New("incomplete checked artefact")
	ErrArtifactMismatch   = errors.New("checked artefact mismatch")
	ErrMissingEvidence    = errors.New("missing required place evidence")
	ErrEvidenceMismatch   = errors.New("place evidence does not match source facts")
	ErrIncompleteEvidence = errors.New("incomplete place evidence")
	ErrMalformedEvidence  = errors.New("malformed place evidence")
	ErrUnsafeEvidence     = errors.New("unsafe place evidence")
)

type Result struct {
	Input *cardwire.CardInput
	Bytes []byte
	ID    string
}

// SourceFacts contains typed source values. Asset and source identities are
// validation and custody fields; Build never copies them into CardInput.
type SourceFacts struct {
	AssetID                 string
	SourceID                string
	CaptureTime             string
	Timezone                *string
	MediaType               string
	MediaSubtypes           []string
	PixelWidth              int64
	PixelHeight             int64
	DurationSeconds         float64
	ImmutableOriginal       ImmutableOriginalFact
	Favorite                bool
	Hidden                  bool
	BurstMember             bool
	Albums                  []AlbumFact
	Location                *LocationFact
	KnownPlace              *KnownPlaceFact
	Camera                  *CameraFact
	Metadata                MetadataFact
	FullCurrent             FullCurrentFact
	RequiredPlaceOperations []string
}

type ImmutableOriginalFact struct {
	ResourceType string
	UTI          string
	Filename     string
	Availability string
	SizeBytes    int64
	SHA256       string
}

type AlbumFact struct {
	Title string
	Kind  string
}

type LocationFact struct {
	Latitude                 float64
	Longitude                float64
	HorizontalAccuracyMeters *float64
}

type KnownPlaceFact struct {
	Name         string
	Relationship string
}

type CameraFact struct {
	Make            string
	Model           string
	LensModel       string
	FocalLengthMM   *float64
	FocalLength35MM *float64
	Aperture        *float64
	ShutterSpeed    *float64
	ISO             *int64
}

type MetadataFact struct {
	RecordSHA256     string
	ProjectionSHA256 string
	ProjectionLines  []string
}

type FullCurrentFact struct {
	Role        string
	MediaType   string
	Orientation int32
	PixelWidth  int64
	PixelHeight int64
	SizeBytes   int64
	SHA256      string
}

// CheckedArtifacts keeps custody values beside checked facts so callers can
// retain their proof. Build validates facts and excludes custody from identity.
type CheckedArtifacts struct {
	ImmutableOriginal CheckedImmutableOriginal
	Metadata          CheckedMetadata
	FullCurrent       CheckedFullCurrent
}

type CheckedImmutableOriginal struct {
	Fact       ImmutableOriginalFact
	ResourceID string
}

type CheckedMetadata struct {
	Fact         MetadataFact
	RecordID     string
	ProjectionID string
}

type CheckedFullCurrent struct {
	Fact        FullCurrentFact
	ProofSHA256 string
}
