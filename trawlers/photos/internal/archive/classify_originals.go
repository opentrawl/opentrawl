package archive

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func (input classifyInput) immutableOriginalIdentity() photos.ImmutableOriginalIdentity {
	resources := make([]photos.Resource, 0, len(input.Resources))
	for _, resource := range input.Resources {
		resources = append(resources, photos.Resource{
			ResourceTypeProjection:          resource.ResourceType,
			UniformTypeIdentifierProjection: resource.UTI,
			OriginalFilename:                resource.OriginalFilename,
		})
	}
	preferred, _ := photos.PreferredOriginalResource(resources)
	return photos.ImmutableOriginalIdentity{
		LocalIdentifier:  input.LocalIdentifier,
		CreationDate:     input.CreationDate,
		Width:            input.Width,
		Height:           input.Height,
		OriginalFilename: preferred.OriginalFilename,
		OriginalUTI:      preferred.UniformTypeIdentifierProjection,
	}
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
