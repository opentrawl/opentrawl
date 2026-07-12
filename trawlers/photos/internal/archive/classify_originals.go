package archive

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

var exportOriginalResource = photos.ExportOriginalResourceThroughApp
var exportCurrentStillResource = photos.ExportCurrentStillThroughApp

func (input classifyInput) originalRequest() photos.OriginalRequest {
	resources := make([]photos.Resource, 0, len(input.Resources))
	packageCandidates := []photos.LocalMediaCandidate{}
	for _, resource := range input.Resources {
		resources = append(resources, photos.Resource{
			Type:             resource.ResourceType,
			UTI:              resource.UTI,
			OriginalFilename: resource.OriginalFilename,
		})
		if resource.ResourceType == "local_original" && strings.TrimSpace(resource.LocalPath) != "" {
			packageCandidates = append(packageCandidates, photos.LocalMediaCandidate{
				Path:  resource.LocalPath,
				Class: "original",
				Size:  resource.FileSize,
			})
		}
	}
	preferred, _ := photos.PreferredOriginalResource(resources)
	return photos.OriginalRequest{
		SourceLibraryID:   input.SourceLibraryID,
		ModificationDate:  input.ModificationDate,
		PackageCandidates: packageCandidates,
		AllowNetwork:      true,
		Query: photos.OriginalExportQuery{
			LocalIdentifier:  input.LocalIdentifier,
			CreationDate:     input.CreationDate,
			Width:            input.Width,
			Height:           input.Height,
			OriginalFilename: preferred.OriginalFilename,
			OriginalUTI:      preferred.UTI,
		},
	}
}

func (input classifyInput) currentStillRequest() (photos.CurrentStillRequest, error) {
	request := photos.CurrentStillRequest{SourceLibraryID: input.SourceLibraryID, AssetUUID: input.LocalIdentifier, AllowNetwork: false}
	if strings.TrimSpace(input.ModificationDate) == "" {
		return request, nil
	}
	modification, err := photos.ParseCurrentStillModification(input.ModificationDate)
	if err != nil {
		return photos.CurrentStillRequest{}, fmt.Errorf("canonicalize current-still modification instant: %w", err)
	}
	request.Modification = modification
	return request, nil
}
