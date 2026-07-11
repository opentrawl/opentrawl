package archive

import (
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

func (input classifyInput) currentStillRequest() photos.CurrentStillRequest {
	return photos.CurrentStillRequest{
		SourceLibraryID:  input.SourceLibraryID,
		AssetUUID:        input.LocalIdentifier,
		ModificationDate: input.ModificationDate,
		AllowNetwork:     false,
	}
}
