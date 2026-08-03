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
	runNode := arguments[0] == "run"
	if runNode {
		arguments = arguments[1:]
		if len(arguments) == 0 {
			return nil, output.UsageError{Err: output.HumanFacingErrorMessage("Photos debug run needs a production node.")}
		}
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
		if runNode {
			return nil, output.UsageError{Err: output.HumanFacingErrorMessage("Run the source node with trawl update photos.")}
		}
		return c.inspectRetainedSourceNode(ctx, req)
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
	debugOptions := updatephotos.Options{
		OpenedArchiveStore:     req.OpenedTrawlerArchiveStore,
		GeoapifyAPIKeyFilePath: c.cfg.GeoapifyAPIKeyFilePath,
		PhotosWorkingRoot:      filepath.Join(archivePaths(req).CacheDir, "photos-working"),
	}
	var result updatephotos.DebugNodeResult
	if runNode {
		result, err = updatephotos.RunAndDebugProductionNode(ctx, debugOptions, nodeName, asset)
	} else {
		result, err = updatephotos.DebugProductionNode(ctx, debugOptions, nodeName, asset)
	}
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse("Photos production node", debugProductionNodeResultFields(result, canonicalReference)...), nil
}

func debugProductionNodeResultFields(result updatephotos.DebugNodeResult, canonicalReference string) []*presentation.TrawlerSpecificCommandDetailPresentationField {
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{photosDetailTextField("Node", string(result.NodeName))}
	if result.Work != nil {
		fields = append(fields, photosDetailTextField("Work", result.Work.String()))
	}
	return append(fields,
		photosDetailCanonicalRecordReferenceField("Photo", canonicalReference),
		photosDetailTextField("Input", result.Input),
		photosDetailTextField("Output", result.Output),
	)
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
		if node.RequiresPhoto {
			details = append(details, "Run: trawl photos debug run "+string(node.Name)+" PHOTO")
		}
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
