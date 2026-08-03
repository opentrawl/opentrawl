package photos

import "context"

type Provider interface {
	OpenSnapshot(ctx context.Context, request SnapshotRequest) (SourceSnapshot, error)
}

type SnapshotRequest struct {
	LibraryPath    string
	WorkingRoot    string
	ReportProgress func(SnapshotProgress)
}

type SnapshotProgressPhase string

const (
	SnapshotProgressCopyingDatabase SnapshotProgressPhase = "copying_database"
	SnapshotProgressReadingAssets   SnapshotProgressPhase = "reading_assets"
)

type SnapshotProgress struct {
	Phase          SnapshotProgressPhase
	AssetsRead     int
	ExpectedAssets int
}

type SourceSnapshot interface {
	Description() SnapshotDescription
	ReadAssetBatches(ctx context.Context, batchSize int, consume func([]Asset) error) (SnapshotReceipt, error)
	Close() error
}

type SnapshotProvider string

const SnapshotProviderPhotosSQLite SnapshotProvider = "photos_sqlite_snapshot"

type PhotosLibraryDatabaseUUID string

type SnapshotDescription struct {
	LibraryPath                        string
	Provider                           SnapshotProvider
	LibraryDatabaseUUID                PhotosLibraryDatabaseUUID
	ExpectedActiveAssetCount           int
	ExpectedUniqueAssetIdentifierCount int
	DatabaseSnapshotFileCount          int
	DatabaseSnapshotBytes              int64
	AlbumJoinTable                     string
}

type SnapshotReceipt struct {
	Description          SnapshotDescription
	Completeness         SnapshotCompleteness
	AssetCount           int
	ResourceCount        int
	AlbumMembershipCount int
	LocationCount        int
}

type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
)

type ResourceKind string

const (
	ResourceKindUnknown ResourceKind = ""
	ResourceKindPhoto   ResourceKind = "photo"
	ResourceKindVideo   ResourceKind = "video"
)

type ResourceAvailability string

const (
	ResourceAvailabilityUnknown ResourceAvailability = "unknown"
	ResourceAvailabilityLocal   ResourceAvailability = "local"
	ResourceAvailabilityRemote  ResourceAvailability = "remote"
)

type Asset struct {
	PhotosSQLiteAssetPrimaryKey int64
	LocalIdentifier             string
	MediaType                   MediaType
	PhotosSQLiteKind            int64
	PhotosSQLiteKindSubtype     int64
	CreationDate                string
	ModificationDate            string
	AddedDate                   string
	TimezoneName                string
	Width                       int64
	Height                      int64
	DurationSeconds             float64
	Favorite                    bool
	Hidden                      bool
	BurstIdentifier             string
	RepresentsBurst             bool
	UniformTypeIdentifier       string
	Filename                    string
	OriginalFilename            string
	Location                    *Location
	Camera                      *Camera
	Resources                   []Resource
	Albums                      []AlbumMembership
}

type Resource struct {
	PhotosSQLiteResourcePrimaryKey int64
	PhotosSQLiteResourceType       int64
	PhotosSQLiteCompactUTI         string
	PhotosSQLiteResourceVersion    int64
	PhotosSQLiteLocalAvailability  int64
	PhotosSQLiteRemoteAvailability int64
	PhotosSQLiteStableHash         string
	PhotosSQLiteFingerprint        string
	Kind                           ResourceKind
	UniformTypeIdentifier          string
	OriginalFilename               string
	Availability                   ResourceAvailability
	FileSize                       int64
	AvailableLocally               bool
	NeedsDownload                  bool
}

type AlbumMembership struct {
	AlbumID                  string
	AlbumTitle               string
	PhotosSQLiteAlbumKind    int64
	PhotosSQLiteAlbumSubtype int64
}

type Location struct {
	Latitude           float64
	Longitude          float64
	Altitude           *float64
	HorizontalAccuracy *float64
}

type Camera struct {
	Make            string
	Model           string
	LensModel       string
	FocalLengthMM   *float64
	FocalLength35MM *float64
	Aperture        *float64
	ShutterSpeed    *float64
	ISO             *int64
}
