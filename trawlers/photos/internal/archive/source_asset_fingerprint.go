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
	sourceAsset := sourceAssetProto(asset)
	fingerprintSourceAsset := *sourceAsset
	fingerprintSourceAsset.PhotosSqliteAssetPrimaryKey = 0
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(&fingerprintSourceAsset)
	if err != nil {
		return "", fmt.Errorf("marshal typed Photos asset fingerprint: %w", err)
	}
	fingerprint := sha256.Sum256(encoded)
	return hex.EncodeToString(fingerprint[:]), nil
}

func sourceAssetProto(asset photos.Asset) *sourcewire.SourceAsset {
	sourceAsset := &sourcewire.SourceAsset{
		PhotosSqliteAssetPrimaryKey: asset.PhotosSQLiteAssetPrimaryKey,
		LocalIdentifier:             asset.LocalIdentifier,
		MediaKind:                   sourceMediaKind(asset.MediaType),
		PhotosSqliteKind:            asset.PhotosSQLiteKind,
		PhotosSqliteKindSubtype:     asset.PhotosSQLiteKindSubtype,
		CreationDate:                asset.CreationDate,
		ModificationDate:            asset.ModificationDate,
		AddedDate:                   asset.AddedDate,
		TimezoneName:                asset.TimezoneName,
		Width:                       asset.Width,
		Height:                      asset.Height,
		DurationSeconds:             asset.DurationSeconds,
		Favorite:                    asset.Favorite,
		Hidden:                      asset.Hidden,
		BurstIdentifier:             asset.BurstIdentifier,
		RepresentsBurst:             asset.RepresentsBurst,
		UniformTypeIdentifier:       asset.UniformTypeIdentifier,
		Filename:                    asset.Filename,
		OriginalFilename:            asset.OriginalFilename,
		Location:                    sourceLocation(asset.Location),
		Camera:                      sourceCamera(asset.Camera),
	}
	for _, resource := range asset.Resources {
		sourceAsset.Resources = append(sourceAsset.Resources, &sourcewire.Resource{
			PhotosSqliteResourcePrimaryKey: resource.PhotosSQLiteResourcePrimaryKey,
			PhotosSqliteResourceType:       resource.PhotosSQLiteResourceType,
			PhotosSqliteCompactUti:         resource.PhotosSQLiteCompactUTI,
			PhotosSqliteResourceVersion:    resource.PhotosSQLiteResourceVersion,
			PhotosSqliteLocalAvailability:  resource.PhotosSQLiteLocalAvailability,
			PhotosSqliteRemoteAvailability: resource.PhotosSQLiteRemoteAvailability,
			PhotosSqliteStableHash:         resource.PhotosSQLiteStableHash,
			PhotosSqliteFingerprint:        resource.PhotosSQLiteFingerprint,
			Kind:                           sourceResourceKind(resource.Kind),
			UniformTypeIdentifier:          resource.UniformTypeIdentifier,
			OriginalFilename:               resource.OriginalFilename,
			Availability:                   sourceResourceAvailability(resource.Availability),
			FileSize:                       resource.FileSize,
			AvailableLocally:               resource.AvailableLocally,
			NeedsDownload:                  resource.NeedsDownload,
		})
	}
	for _, album := range asset.Albums {
		sourceAsset.Albums = append(sourceAsset.Albums, &sourcewire.AlbumMembership{
			AlbumId:                  album.AlbumID,
			AlbumTitle:               album.AlbumTitle,
			PhotosSqliteAlbumKind:    album.PhotosSQLiteAlbumKind,
			PhotosSqliteAlbumSubtype: album.PhotosSQLiteAlbumSubtype,
		})
	}
	return sourceAsset
}

func photosAssetFromSourceAssetProto(sourceAsset *sourcewire.SourceAsset) photos.Asset {
	asset := photos.Asset{
		PhotosSQLiteAssetPrimaryKey: sourceAsset.GetPhotosSqliteAssetPrimaryKey(),
		LocalIdentifier:             sourceAsset.GetLocalIdentifier(),
		MediaType:                   photosMediaTypeFromSource(sourceAsset.GetMediaKind()),
		PhotosSQLiteKind:            sourceAsset.GetPhotosSqliteKind(),
		PhotosSQLiteKindSubtype:     sourceAsset.GetPhotosSqliteKindSubtype(),
		CreationDate:                sourceAsset.GetCreationDate(),
		ModificationDate:            sourceAsset.GetModificationDate(),
		AddedDate:                   sourceAsset.GetAddedDate(),
		TimezoneName:                sourceAsset.GetTimezoneName(),
		Width:                       sourceAsset.GetWidth(),
		Height:                      sourceAsset.GetHeight(),
		DurationSeconds:             sourceAsset.GetDurationSeconds(),
		Favorite:                    sourceAsset.GetFavorite(),
		Hidden:                      sourceAsset.GetHidden(),
		BurstIdentifier:             sourceAsset.GetBurstIdentifier(),
		RepresentsBurst:             sourceAsset.GetRepresentsBurst(),
		UniformTypeIdentifier:       sourceAsset.GetUniformTypeIdentifier(),
		Filename:                    sourceAsset.GetFilename(),
		OriginalFilename:            sourceAsset.GetOriginalFilename(),
		Location:                    photosLocationFromSource(sourceAsset.GetLocation()),
		Camera:                      photosCameraFromSource(sourceAsset.GetCamera()),
	}
	for _, resource := range sourceAsset.GetResources() {
		asset.Resources = append(asset.Resources, photos.Resource{
			PhotosSQLiteResourcePrimaryKey: resource.GetPhotosSqliteResourcePrimaryKey(),
			PhotosSQLiteResourceType:       resource.GetPhotosSqliteResourceType(),
			PhotosSQLiteCompactUTI:         resource.GetPhotosSqliteCompactUti(),
			PhotosSQLiteResourceVersion:    resource.GetPhotosSqliteResourceVersion(),
			PhotosSQLiteLocalAvailability:  resource.GetPhotosSqliteLocalAvailability(),
			PhotosSQLiteRemoteAvailability: resource.GetPhotosSqliteRemoteAvailability(),
			PhotosSQLiteStableHash:         resource.GetPhotosSqliteStableHash(),
			PhotosSQLiteFingerprint:        resource.GetPhotosSqliteFingerprint(),
			Kind:                           photosResourceKindFromSource(resource.GetKind()),
			UniformTypeIdentifier:          resource.GetUniformTypeIdentifier(),
			OriginalFilename:               resource.GetOriginalFilename(),
			Availability:                   photosResourceAvailabilityFromSource(resource.GetAvailability()),
			FileSize:                       resource.GetFileSize(),
			AvailableLocally:               resource.GetAvailableLocally(),
			NeedsDownload:                  resource.GetNeedsDownload(),
		})
	}
	for _, album := range sourceAsset.GetAlbums() {
		asset.Albums = append(asset.Albums, photos.AlbumMembership{
			AlbumID:                  album.GetAlbumId(),
			AlbumTitle:               album.GetAlbumTitle(),
			PhotosSQLiteAlbumKind:    album.GetPhotosSqliteAlbumKind(),
			PhotosSQLiteAlbumSubtype: album.GetPhotosSqliteAlbumSubtype(),
		})
	}
	return asset
}

func photosMediaTypeFromSource(mediaKind sourcewire.MediaKind) photos.MediaType {
	switch mediaKind {
	case sourcewire.MediaKind_MEDIA_KIND_IMAGE:
		return photos.MediaTypeImage
	case sourcewire.MediaKind_MEDIA_KIND_VIDEO:
		return photos.MediaTypeVideo
	default:
		return photos.MediaTypeOther
	}
}

func photosResourceKindFromSource(resourceKind sourcewire.ResourceKind) photos.ResourceKind {
	switch resourceKind {
	case sourcewire.ResourceKind_RESOURCE_KIND_PHOTO:
		return photos.ResourceKindPhoto
	case sourcewire.ResourceKind_RESOURCE_KIND_VIDEO:
		return photos.ResourceKindVideo
	default:
		return photos.ResourceKindUnknown
	}
}

func photosResourceAvailabilityFromSource(availability sourcewire.ResourceAvailability) photos.ResourceAvailability {
	switch availability {
	case sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_LOCAL:
		return photos.ResourceAvailabilityLocal
	case sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_REMOTE:
		return photos.ResourceAvailabilityRemote
	default:
		return photos.ResourceAvailabilityUnknown
	}
}

func photosLocationFromSource(location *sourcewire.Location) *photos.Location {
	if location == nil {
		return nil
	}
	return &photos.Location{
		Latitude:           location.GetLatitude(),
		Longitude:          location.GetLongitude(),
		Altitude:           location.Altitude,
		HorizontalAccuracy: location.HorizontalAccuracy,
	}
}

func photosCameraFromSource(camera *sourcewire.Camera) *photos.Camera {
	if camera == nil {
		return nil
	}
	return &photos.Camera{
		Make:            camera.GetMake(),
		Model:           camera.GetModel(),
		LensModel:       camera.GetLensModel(),
		FocalLengthMM:   camera.FocalLengthMm,
		FocalLength35MM: camera.FocalLength_35Mm,
		Aperture:        camera.Aperture,
		ShutterSpeed:    camera.ShutterSpeed,
		ISO:             camera.Iso,
	}
}

func sourceMediaKind(mediaType photos.MediaType) sourcewire.MediaKind {
	switch mediaType {
	case photos.MediaTypeImage:
		return sourcewire.MediaKind_MEDIA_KIND_IMAGE
	case photos.MediaTypeVideo:
		return sourcewire.MediaKind_MEDIA_KIND_VIDEO
	default:
		return sourcewire.MediaKind_MEDIA_KIND_OTHER
	}
}

func sourceResourceKind(kind photos.ResourceKind) sourcewire.ResourceKind {
	switch kind {
	case photos.ResourceKindPhoto:
		return sourcewire.ResourceKind_RESOURCE_KIND_PHOTO
	case photos.ResourceKindVideo:
		return sourcewire.ResourceKind_RESOURCE_KIND_VIDEO
	default:
		return sourcewire.ResourceKind_RESOURCE_KIND_UNSPECIFIED
	}
}

func sourceResourceAvailability(availability photos.ResourceAvailability) sourcewire.ResourceAvailability {
	switch availability {
	case photos.ResourceAvailabilityLocal:
		return sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_LOCAL
	case photos.ResourceAvailabilityRemote:
		return sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_REMOTE
	default:
		return sourcewire.ResourceAvailability_RESOURCE_AVAILABILITY_UNKNOWN
	}
}

func sourceLocation(location *photos.Location) *sourcewire.Location {
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

func sourceCamera(camera *photos.Camera) *sourcewire.Camera {
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
