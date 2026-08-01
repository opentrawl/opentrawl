package photos

import "context"

type Provider interface {
	Snapshot(ctx context.Context, libraryPath string) (LibrarySnapshot, error)
}

type LibrarySnapshot struct {
	LibraryPath         string               `json:"library_path"`
	Provider            string               `json:"provider"`
	PhotosVersion       string               `json:"photos_version"`
	AuthorizationStatus string               `json:"authorization_status,omitempty"`
	Completeness        SnapshotCompleteness `json:"completeness"`
	Metadata            map[string]any       `json:"metadata,omitempty"`
	Assets              []Asset              `json:"assets"`
}

type Asset struct {
	LocalIdentifier  string            `json:"local_identifier"`
	MediaType        string            `json:"media_type"`
	MediaSubtypes    string            `json:"media_subtypes"`
	CreationDate     string            `json:"creation_date"`
	ModificationDate string            `json:"modification_date"`
	AddedDate        string            `json:"added_date"`
	TimezoneName     string            `json:"timezone_name"`
	Width            int64             `json:"width"`
	Height           int64             `json:"height"`
	DurationSeconds  float64           `json:"duration_seconds"`
	Favorite         bool              `json:"favorite"`
	Hidden           bool              `json:"hidden"`
	BurstIdentifier  string            `json:"burst_identifier"`
	RepresentsBurst  bool              `json:"represents_burst"`
	Location         *Location         `json:"location,omitempty"`
	Camera           *Camera           `json:"camera,omitempty"`
	Resources        []Resource        `json:"resources,omitempty"`
	Albums           []AlbumMembership `json:"albums,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
}

type Resource struct {
	PhotosSQLiteResourcePrimaryKey  int64          `json:"photos_sqlite_resource_primary_key"`
	PhotosSQLiteResourceType        int64          `json:"photos_sqlite_resource_type"`
	PhotosSQLiteCompactUTI          string         `json:"photos_sqlite_compact_uti"`
	PhotosSQLiteResourceVersion     int64          `json:"photos_sqlite_resource_version"`
	PhotosSQLiteLocalAvailability   int64          `json:"photos_sqlite_local_availability"`
	PhotosSQLiteRemoteAvailability  int64          `json:"photos_sqlite_remote_availability"`
	PhotosSQLiteStableHash          string         `json:"photos_sqlite_stable_hash,omitempty"`
	PhotosSQLiteFingerprint         string         `json:"photos_sqlite_fingerprint,omitempty"`
	ResourceTypeProjection          string         `json:"resource_type_projection"`
	UniformTypeIdentifierProjection string         `json:"uniform_type_identifier_projection"`
	OriginalFilename                string         `json:"original_filename"`
	LocalPath                       string         `json:"local_path,omitempty"`
	AvailabilityProjection          string         `json:"availability_projection"`
	FileSize                        int64          `json:"file_size,omitempty"`
	AvailableLocally                bool           `json:"available_locally"`
	NeedsDownload                   bool           `json:"needs_download"`
	Metadata                        map[string]any `json:"metadata,omitempty"`
}

type AlbumMembership struct {
	AlbumID    string `json:"album_id"`
	AlbumTitle string `json:"album_title"`
	AlbumKind  string `json:"album_kind"`
}

type Location struct {
	Latitude           float64  `json:"latitude"`
	Longitude          float64  `json:"longitude"`
	Altitude           *float64 `json:"altitude,omitempty"`
	HorizontalAccuracy *float64 `json:"horizontal_accuracy,omitempty"`
}

type Camera struct {
	Make            string   `json:"make,omitempty"`
	Model           string   `json:"model,omitempty"`
	LensModel       string   `json:"lens_model,omitempty"`
	FocalLengthMM   *float64 `json:"focal_length_mm,omitempty"`
	FocalLength35MM *float64 `json:"focal_length_35mm,omitempty"`
	Aperture        *float64 `json:"aperture,omitempty"`
	ShutterSpeed    *float64 `json:"shutter_speed,omitempty"`
	ISO             *int64   `json:"iso,omitempty"`
}
