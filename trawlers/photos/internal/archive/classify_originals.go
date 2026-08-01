package archive

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

type indexedImmutableOriginalResourceEvidence struct {
	MatchesPhotoKitIdentity      bool
	UnambiguousPositiveByteCount int64
}

func (input classifyInput) indexedImmutableOriginalResourceEvidenceForPhotoKitIdentity(
	photoKitOriginalFilename string,
	photoKitOriginalUniformTypeIdentifier string,
) indexedImmutableOriginalResourceEvidence {
	if strings.TrimSpace(photoKitOriginalFilename) == "" || strings.TrimSpace(photoKitOriginalUniformTypeIdentifier) == "" {
		return indexedImmutableOriginalResourceEvidence{}
	}
	evidence := indexedImmutableOriginalResourceEvidence{}
	for _, indexedResource := range input.Resources {
		if indexedResource.ResourceType != "photo" ||
			indexedResource.OriginalFilename != photoKitOriginalFilename ||
			indexedResource.UTI != photoKitOriginalUniformTypeIdentifier {
			continue
		}
		evidence.MatchesPhotoKitIdentity = true
		if indexedResource.FileSize <= 0 {
			continue
		}
		if evidence.UnambiguousPositiveByteCount == 0 {
			evidence.UnambiguousPositiveByteCount = indexedResource.FileSize
			continue
		}
		if evidence.UnambiguousPositiveByteCount != indexedResource.FileSize {
			evidence.UnambiguousPositiveByteCount = 0
			return evidence
		}
	}
	return evidence
}

func (input classifyInput) currentStillRequest() (photos.CurrentStillRequest, error) {
	request := photos.CurrentStillRequest{AllowNetwork: false}
	if strings.TrimSpace(input.ModificationDate) == "" {
		return photos.CurrentStillRequest{}, fmt.Errorf("current-rendered image requires the indexed Photos modification instant")
	}
	modification, err := photos.ParseCurrentStillModification(input.ModificationDate)
	if err != nil {
		return photos.CurrentStillRequest{}, fmt.Errorf("canonicalize current-still modification instant: %w", err)
	}
	request.Freshness, err = photos.CurrentStillFreshnessForModification(modification)
	if err != nil {
		return photos.CurrentStillRequest{}, err
	}
	return request, nil
}
