package photos

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/updatephotos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func (c *Crawler) debugProductionNode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	arguments := req.TrawlerCommandPositionalArguments
	if len(arguments) == 0 {
		return debugProductionNodeListResponse(), nil
	}
	nodeName, err := updatephotos.ParseProductionNodeName(arguments[0])
	if err != nil {
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage(err.Error())}
	}
	selectedNode := productionNode(nodeName)
	if selectedNode.RequiresPhoto && len(arguments) != 2 {
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage(fmt.Sprintf("Photos debug %s needs a photo link.", nodeName))}
	}
	if !selectedNode.RequiresPhoto && len(arguments) != 1 {
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage(fmt.Sprintf("Photos debug %s does not take a photo link.", nodeName))}
	}
	if nodeName == updatephotos.ProductionNodeSource {
		return c.inspectRetainedSourceNode(ctx, req)
	}
	if !selectedNode.RequiresPhoto {
		result, err := updatephotos.DebugProductionNode(ctx, updatephotos.Options{
			OpenedArchiveStore: req.OpenedTrawlerArchiveStore,
		}, nodeName, archive.PhotoUpdateAsset{})
		if err != nil {
			return nil, err
		}
		return photosDetailCommandResponse(
			"Photos production node",
			photosDetailTextField("Node", string(result.NodeName)),
			photosDetailTextField("Input", result.Input),
			photosDetailTextField("Output", result.Output),
		), nil
	}

	localReferenceText, _, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(arguments[1], "photos")
	if err != nil {
		return nil, err
	}
	canonicalReference, err := c.resolveInputRef(ctx, req, trawlkit.NewLocalTrawlerShortReference(localReferenceText))
	if err != nil {
		return nil, err
	}
	asset, err := archive.LoadPhotoUpdateAsset(ctx, req.OpenedTrawlerArchiveStore, archive.PhotoAssetID(archive.AssetID(canonicalReference)))
	if err != nil {
		return nil, err
	}
	result, err := updatephotos.DebugProductionNode(ctx, updatephotos.Options{
		OpenedArchiveStore:     req.OpenedTrawlerArchiveStore,
		GeoapifyAPIKeyFilePath: c.cfg.GeoapifyAPIKeyFilePath,
		PhotosWorkingRoot:      filepath.Join(archivePaths(req).CacheDir, "photos-working"),
	}, nodeName, asset)
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse(
		"Photos production node",
		photosDetailTextField("Node", string(result.NodeName)),
		photosDetailCanonicalRecordReferenceField("Photo", canonicalReference),
		photosDetailTextField("Input", result.Input),
		photosDetailTextField("Output", result.Output),
	), nil
}

func productionNode(name updatephotos.ProductionNodeName) updatephotos.ProductionNode {
	for _, node := range updatephotos.ProductionNodesInDependencyOrder() {
		if node.Name == name {
			return node
		}
	}
	return updatephotos.ProductionNode{}
}

func debugProductionNodeListResponse() *command.TrawlerCommandResponse {
	fields := make([]*presentation.TrawlerSpecificCommandDetailPresentationField, 0, len(updatephotos.ProductionNodesInDependencyOrder()))
	for _, node := range updatephotos.ProductionNodesInDependencyOrder() {
		invocation := "trawl photos debug " + string(node.Name)
		if node.RequiresPhoto {
			invocation += " PHOTO"
		}
		details := []string{}
		if len(node.Dependencies) != 0 {
			dependencyNames := make([]string, len(node.Dependencies))
			for index, dependency := range node.Dependencies {
				dependencyNames[index] = string(dependency)
			}
			details = append(details, "Needs: "+strings.Join(dependencyNames, ", "))
		}
		details = append(details, invocation)
		fields = append(fields, photosDetailTextField(string(node.Name), strings.Join(details, "\n")))
	}
	return photosDetailCommandResponse("Photos production DAG", fields...)
}

func (c *Crawler) inspectRetainedSourceNode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	var currentAssetCount int64
	if err := req.OpenedTrawlerArchiveStore.DB().QueryRowContext(ctx, `select count(*) from asset where source_state='current'`).Scan(&currentAssetCount); err != nil {
		return nil, err
	}
	return photosDetailCommandResponse(
		"Photos production node",
		photosDetailTextField("Node", string(updatephotos.ProductionNodeSource)),
		photosDetailTextField("Input", "The retained Apple Photos source index"),
		photosDetailTextField("Output", fmt.Sprintf("Current indexed assets: %d", currentAssetCount)),
	), nil
}
