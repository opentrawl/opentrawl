package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	sourcewire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/source"
	"google.golang.org/protobuf/proto"
)

func assetFingerprint(asset photos.Asset) (string, error) {
	receipt := &sourcewire.AssetFingerprintReceipt{
		LocalIdentifier:         asset.LocalIdentifier,
		MediaKind:               sourceFingerprintMediaKind(asset.MediaType),
		PhotosSqliteKind:        asset.PhotosSQLiteKind,
		PhotosSqliteKindSubtype: asset.PhotosSQLiteKindSubtype,
		CreationDate:            asset.CreationDate,
		ModificationDate:        asset.ModificationDate,
		AddedDate:               asset.AddedDate,
		TimezoneName:            asset.TimezoneName,
		Width:                   asset.Width,
		Height:                  asset.Height,
		DurationSeconds:         asset.DurationSeconds,
		Favorite:                asset.Favorite,
		Hidden:                  asset.Hidden,
		BurstIdentifier:         asset.BurstIdentifier,
		RepresentsBurst:         asset.RepresentsBurst,
		UniformTypeIdentifier:   asset.UniformTypeIdentifier,
		Filename:                asset.Filename,
		OriginalFilename:        asset.OriginalFilename,
		Location:                sourceFingerprintLocation(asset.Location),
		Camera:                  sourceFingerprintCamera(asset.Camera),
	}
	for _, resource := range asset.Resources {
		receipt.Resources = append(receipt.Resources, &sourcewire.Resource{
			PhotosSqliteResourcePrimaryKey: resource.PhotosSQLiteResourcePrimaryKey,
			PhotosSqliteResourceType:       resource.PhotosSQLiteResourceType,
			PhotosSqliteCompactUti:         resource.PhotosSQLiteCompactUTI,
			PhotosSqliteResourceVersion:    resource.PhotosSQLiteResourceVersion,
			PhotosSqliteLocalAvailability:  resource.PhotosSQLiteLocalAvailability,
			PhotosSqliteRemoteAvailability: resource.PhotosSQLiteRemoteAvailability,
			PhotosSqliteStableHash:         resource.PhotosSQLiteStableHash,
			PhotosSqliteFingerprint:        resource.PhotosSQLiteFingerprint,
			Kind:                           sourceFingerprintResourceKind(resource.Kind),
			UniformTypeIdentifier:          resource.UniformTypeIdentifier,
			OriginalFilename:               resource.OriginalFilename,
			Availability:                   sourceFingerprintResourceAvailability(resource.Availability),
			FileSize:                       resource.FileSize,
			AvailableLocally:               resource.AvailableLocally,
			NeedsDownload:                  resource.NeedsDownload,
		})
	}
	for _, album := range asset.Albums {
		receipt.Albums = append(receipt.Albums, &sourcewire.AlbumMembership{
			AlbumId:                  album.AlbumID,
			AlbumTitle:               album.AlbumTitle,
			PhotosSqliteAlbumKind:    album.PhotosSQLiteAlbumKind,
			PhotosSqliteAlbumSubtype: album.PhotosSQLiteAlbumSubtype,
		})
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("marshal typed Photos asset fingerprint receipt: %w", err)
	}
	fingerprint := sha256.Sum256(encoded)
	return hex.EncodeToString(fingerprint[:]), nil
}

func sourceFingerprintMediaKind(mediaType photos.MediaType) sourcewire.MediaKind {
	switch mediaType {
	case photos.MediaTypeImage:
		return sourcewire.MediaKind_MEDIA_KIND_IMAGE
	case photos.MediaTypeVideo:
		return sourcewire.MediaKind_MEDIA_KIND_VIDEO
	default:
		return sourcewire.MediaKind_MEDIA_KIND_OTHER
	}
}

func sourceFingerprintResourceKind(kind photos.ResourceKind) sourcewire.ResourceKind {
	switch kind {
	case photos.ResourceKindPhoto:
		return sourcewire.ResourceKind_RESOURCE_KIND_PHOTO
	case photos.ResourceKindVideo:
		return sourcewire.ResourceKind_RESOURCE_KIND_VIDEO
	default:
		return sourcewire.ResourceKind_RESOURCE_KIND_UNSPECIFIED
	}
}

func sourceFingerprintResourceAvailability(availability photos.ResourceAvailability) sourcewire.ResourceAvailability {
	switch availability {
	case photos.ResourceAvailabilityLocal:
		return sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_LOCAL
	case photos.ResourceAvailabilityRemote:
		return sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_REMOTE
	default:
		return sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_UNKNOWN
	}
}

func sourceFingerprintLocation(location *photos.Location) *sourcewire.Location {
	if location == nil {
		return nil
	}
	return &sourcewire.Location{
		Latitude:           location.Latitude,
		Longitude:          location.Longitude,
		Altitude:           location.Altitude,
		HorizontalAccuracy: location.HorizontalAccuracy,
	}
}

func sourceFingerprintCamera(camera *photos.Camera) *sourcewire.Camera {
	if camera == nil {
		return nil
	}
	return &sourcewire.Camera{
		Make:             camera.Make,
		Model:            camera.Model,
		LensModel:        camera.LensModel,
		FocalLengthMm:    camera.FocalLengthMM,
		FocalLength_35Mm: camera.FocalLength35MM,
		Aperture:         camera.Aperture,
		ShutterSpeed:     camera.ShutterSpeed,
		Iso:              camera.ISO,
	}
}
