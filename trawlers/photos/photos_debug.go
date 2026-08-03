package photos

import (
	"context"
	"path/filepath"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/updatephotos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func (c *Crawler) debugProductionNode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	return c.productionNodeCommand(ctx, req, false)
}

func (c *Crawler) runProductionNode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	return c.productionNodeCommand(ctx, req, true)
}

func (c *Crawler) productionNodeCommand(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, runNode bool) (*command.TrawlerCommandResponse, error) {
	arguments := req.TrawlerCommandPositionalArguments
	if len(arguments) == 0 {
		return debugProductionNodeListResponse()
	}
	nodeName, err := updatephotos.ParseProductionNodeName(arguments[0])
	if err != nil {
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage(err.Error())}
	}
	selectedNode := productionNode(nodeName)
	commandName := "debug"
	if runNode {
		commandName = "run"
	}
	if selectedNode.RequiresPhoto && len(arguments) != 2 {
		message, renderErr := renderPhotosDebugText("photo-required", photosDebugNodeCommandText{Command: commandName, Node: nodeName})
		if renderErr != nil {
			return nil, renderErr
		}
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage(message)}
	}
	if !selectedNode.RequiresPhoto && len(arguments) != 1 {
		message, renderErr := renderPhotosDebugText("photo-not-accepted", photosDebugNodeCommandText{Command: commandName, Node: nodeName})
		if renderErr != nil {
			return nil, renderErr
		}
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage(message)}
	}
	if nodeName == updatephotos.ProductionNodeSource {
		if runNode {
			return c.runSourceProductionNode(ctx, req)
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
		debugOptions.Observe = observePhotosUpdate(req)
		result, err = updatephotos.RunAndDebugProductionNode(ctx, debugOptions, nodeName, asset)
	} else {
		result, err = updatephotos.DebugProductionNode(ctx, debugOptions, nodeName, asset)
	}
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse("Photos production node", debugProductionNodeResultFields(result, canonicalReference)...), nil
}

func (c *Crawler) runSourceProductionNode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	result, err := c.updatePhotosSourceIndex(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info(string(photosLogUpdateWritten), renderPhotosObservation(req, photosMessageSourceDone, photosObservationTemplateData{Source: result}))
	}
	return photosDetailCommandResponse(
		"Photos production node",
		photosDetailTextField("Node", string(updatephotos.ProductionNodeSource)),
		photosDetailTextField("Provider", result.Provider),
		photosDetailTextField("Completeness", result.SnapshotCompleteness),
		photosDetailUnsignedCountField("Assets", int64(result.AssetsSeen)),
		photosDetailUnsignedCountField("New", int64(result.AssetsNew)),
		photosDetailUnsignedCountField("Changed", int64(result.AssetsChanged)),
		photosDetailUnsignedCountField("Unchanged", int64(result.AssetsUnchanged)),
		photosDetailUnsignedCountField("Missing", int64(result.PreviouslySeenMissing)),
	), nil
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

func debugProductionNodeListResponse() (*command.TrawlerCommandResponse, error) {
	fields := make([]*presentation.TrawlerSpecificCommandDetailPresentationField, 0, len(updatephotos.ProductionNodesInDependencyOrder()))
	for _, node := range updatephotos.ProductionNodesInDependencyOrder() {
		dependencyNames := make([]string, len(node.Dependencies))
		if len(node.Dependencies) != 0 {
			for index, dependency := range node.Dependencies {
				dependencyNames[index] = string(dependency)
			}
		}
		details, err := renderPhotosDebugText("node-details", photosDebugNodeDetailsText{Node: node.Name, Dependencies: dependencyNames, RequiresPhoto: node.RequiresPhoto})
		if err != nil {
			return nil, err
		}
		fields = append(fields, photosDetailTextField(string(node.Name), details))
	}
	return photosDetailCommandResponse("Photos production DAG", fields...), nil
}

func (c *Crawler) inspectRetainedSourceNode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	var currentAssetCount int64
	if err := req.OpenedTrawlerArchiveStore.DB().QueryRowContext(ctx, `select count(*) from asset where source_state='current'`).Scan(&currentAssetCount); err != nil {
		return nil, err
	}
	input, err := renderPhotosDebugText("source-input", nil)
	if err != nil {
		return nil, err
	}
	output, err := renderPhotosDebugText("source-output", currentAssetCount)
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse(
		"Photos production node",
		photosDetailTextField("Node", string(updatephotos.ProductionNodeSource)),
		photosDetailTextField("Input", input),
		photosDetailTextField("Output", output),
	), nil
}
