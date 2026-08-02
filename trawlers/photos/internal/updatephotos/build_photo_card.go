package updatephotos

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/luna"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type photoCardDerivationInputs struct {
	SourceFingerprint                 archive.PhotoSourceFingerprint
	CurrentRenderedStillSHA256        []byte
	ImmutableOriginalImageFactsSHA256 []byte
	ExtractedPhotoTextSHA256          []byte
	LocationEvidenceSHA256            []byte
	HumanReadableInstructions         string
	StructuredOutputSchemaJSON        []byte
	ModelIdentifier                   string
}

func (worker *photoAssetWorker) generatePhotoCard(ctx context.Context, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence, extractedPhotoText *cardwire.PhotoOpticalCharacterRecognition, locationOutcome *locationwire.ComposePhotoLocationEvidenceOutcome, locationEvidence photocard.HumanReadableLocationEvidence, checkedEvidence string) (*cardwire.PhotoCard, []byte, []byte, error) {
	runner := worker.runner
	locationBytes := []byte(nil)
	if locationOutcome != nil {
		encodedLocation, err := proto.Marshal(locationOutcome)
		if err != nil {
			return nil, nil, nil, err
		}
		locationBytes = encodedLocation
	}
	locationDigest := sha256.Sum256(locationBytes)
	instructions, err := photocard.BuildPhotoCardInstructions(checkedEvidence, extractedPhotoText)
	if err != nil {
		return nil, nil, nil, err
	}
	structuredOutputSchemaJSON, err := photocard.PhotoCardSemanticSectionsStructuredOutputSchemaJSON()
	if err != nil {
		return nil, nil, nil, err
	}
	structuredOutputSchema, err := luna.NewStructuredOutputSchema(structuredOutputSchemaJSON)
	if err != nil {
		return nil, nil, nil, err
	}
	extractedPhotoTextBytes, err := proto.Marshal(extractedPhotoText)
	if err != nil {
		return nil, nil, nil, err
	}
	extractedPhotoTextDigest := sha256.Sum256(extractedPhotoTextBytes)
	inputSHA256 := photoCardDerivationInputs{
		SourceFingerprint:                 asset.SourceFingerprint,
		CurrentRenderedStillSHA256:        mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(),
		ImmutableOriginalImageFactsSHA256: mediaEvidence.ImmutableOriginalFacts.GetSha256(),
		ExtractedPhotoTextSHA256:          extractedPhotoTextDigest[:],
		LocationEvidenceSHA256:            locationDigest[:],
		HumanReadableInstructions:         instructions,
		StructuredOutputSchemaJSON:        structuredOutputSchemaJSON,
		ModelIdentifier:                   luna.ModelGPT56Luna,
	}.SHA256()
	if err := archive.RetainPhotoCardGenerationRequest(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, instructions); err != nil {
		return nil, nil, nil, err
	}
	if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, archive.PhotoModelGenerationStateRequestRetained, "", "", "", time.Now()); err != nil {
		return nil, nil, nil, err
	}
	retained, found, err := archive.LoadRetainedPhotoCardGeneration(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	if err != nil {
		return nil, nil, nil, err
	}
	response := retained.ResponseBody
	if !found || len(response) == 0 || retained.ResponseRejected {
		client, err := worker.ensureLunaClient(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		imageBytes, err := mediaEvidence.CurrentRenderedStill.Read()
		if err != nil {
			return nil, nil, nil, err
		}
		generation, err := client.Generate(ctx, luna.GenerationRequest{
			Instructions: instructions, Image: imageBytes, ImageMediaType: lunaImageMediaType(mediaEvidence.CurrentRenderedStill.Outcome.GetUniformTypeIdentifier()), OutputSchema: structuredOutputSchema,
			TransmissionStarted: func(threadIdentifier string) error {
				return archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, archive.PhotoModelGenerationStateTransmissionStarted, threadIdentifier, "", "", time.Now())
			},
			ResponseReceived: func(received luna.GenerationResult) error {
				return archive.RetainPhotoCardGenerationResponse(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, received.ThreadID, received.TurnID, received.RawStructuredOutputJSON, photoModelGenerationUsage(received), time.Now())
			},
		})
		if err != nil {
			if retainErr := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, archive.PhotoModelGenerationStateFailed, "", "", err.Error(), time.Now()); retainErr != nil {
				return nil, nil, nil, errors.Join(err, retainErr)
			}
			return nil, nil, nil, &AssetDeferredError{Reason: "Luna PhotoCard generation remains retryable"}
		}
		response = generation.RawStructuredOutputJSON
		retained.ThreadIdentifier = generation.ThreadID
		retained.TurnIdentifier = generation.TurnID
	}
	semanticSections := new(cardwire.PhotoCardSemanticSections)
	if err := protojson.Unmarshal(response, semanticSections); err != nil {
		return nil, nil, nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, retained.ThreadIdentifier, retained.TurnIdentifier, errors.New("Luna PhotoCard response did not match the generated Protobuf schema"))
	}
	card, err := photocard.ComposePhotoCard(extractedPhotoText, semanticSections, locationEvidence.SuppliedCandidates)
	if err != nil {
		return nil, nil, nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, retained.ThreadIdentifier, retained.TurnIdentifier, err)
	}
	validationErr := photocard.ValidateModelResult(card, locationEvidence.SuppliedCandidates)
	if validationErr == nil {
		if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, archive.PhotoModelGenerationStateSucceeded, retained.ThreadIdentifier, retained.TurnIdentifier, "", time.Now()); err != nil {
			return nil, nil, nil, err
		}
		if locationOutcome == nil {
			return card, inputSHA256, nil, nil
		}
		return card, inputSHA256, locationDigest[:], nil
	}
	if !photocard.NeedsDescriptionsOnlyRepair(card, locationEvidence.SuppliedCandidates) {
		return nil, nil, nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseSemanticCard, retained.ThreadIdentifier, retained.TurnIdentifier, validationErr)
	}
	return worker.repairPhotoCardDescriptions(ctx, asset, mediaEvidence, locationOutcome, locationEvidence, checkedEvidence, inputSHA256, locationDigest, retained, card)
}

func (worker *photoAssetWorker) repairPhotoCardDescriptions(ctx context.Context, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence, locationOutcome *locationwire.ComposePhotoLocationEvidenceOutcome, locationEvidence photocard.HumanReadableLocationEvidence, checkedEvidence string, inputSHA256 []byte, locationDigest [sha256.Size]byte, retained archive.RetainedPhotoCardGeneration, card *cardwire.PhotoCard) (*cardwire.PhotoCard, []byte, []byte, error) {
	runner := worker.runner
	repairInstructions, err := photocard.BuildDescriptionsRepairInstructions(checkedEvidence, card)
	if err != nil {
		return nil, nil, nil, err
	}
	repairSchema, err := photocard.DescriptionsRepairStructuredOutputSchema()
	if err != nil {
		return nil, nil, nil, err
	}
	imageBytes, err := mediaEvidence.CurrentRenderedStill.Read()
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := worker.ensureLunaClient(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	repairResponse := retained.DescriptionsRepairResponseBody
	if len(repairResponse) == 0 || retained.DescriptionsRepairResponseRejected {
		if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseDescriptionRepair, archive.PhotoModelGenerationStateRequestRetained, "", "", "", time.Now()); err != nil {
			return nil, nil, nil, err
		}
		repairGeneration, err := client.Generate(ctx, luna.GenerationRequest{
			Instructions: repairInstructions, Image: imageBytes, ImageMediaType: lunaImageMediaType(mediaEvidence.CurrentRenderedStill.Outcome.GetUniformTypeIdentifier()), OutputSchema: repairSchema,
			TransmissionStarted: func(threadIdentifier string) error {
				return archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseDescriptionRepair, archive.PhotoModelGenerationStateTransmissionStarted, threadIdentifier, "", "", time.Now())
			},
			ResponseReceived: func(received luna.GenerationResult) error {
				return archive.RetainPhotoCardDescriptionsRepair(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, repairInstructions, received.ThreadID, received.TurnID, received.RawStructuredOutputJSON, photoModelGenerationUsage(received), time.Now())
			},
		})
		if err != nil {
			if retainErr := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseDescriptionRepair, archive.PhotoModelGenerationStateFailed, "", "", err.Error(), time.Now()); retainErr != nil {
				return nil, nil, nil, errors.Join(err, retainErr)
			}
			return nil, nil, nil, &AssetDeferredError{Reason: "Luna PhotoCard description repair remains retryable"}
		}
		repairResponse = repairGeneration.RawStructuredOutputJSON
		retained.DescriptionsRepairThreadID = repairGeneration.ThreadID
		retained.DescriptionsRepairTurnID = repairGeneration.TurnID
	}
	repairedDescriptions := new(cardwire.PhotoDescriptions)
	if err := protojson.Unmarshal(repairResponse, repairedDescriptions); err != nil {
		return nil, nil, nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseDescriptionRepair, retained.DescriptionsRepairThreadID, retained.DescriptionsRepairTurnID, errors.New("Luna PhotoCard descriptions repair did not match the generated Protobuf schema"))
	}
	merged, err := photocard.MergeDescriptionsRepair(card, repairedDescriptions, locationEvidence.SuppliedCandidates)
	if err != nil {
		return nil, nil, nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseDescriptionRepair, retained.DescriptionsRepairThreadID, retained.DescriptionsRepairTurnID, err)
	}
	if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseDescriptionRepair, archive.PhotoModelGenerationStateSucceeded, retained.DescriptionsRepairThreadID, retained.DescriptionsRepairTurnID, "", time.Now()); err != nil {
		return nil, nil, nil, err
	}
	if locationOutcome == nil {
		return merged, inputSHA256, nil, nil
	}
	return merged, inputSHA256, locationDigest[:], nil
}

func (inputs photoCardDerivationInputs) SHA256() []byte {
	digest := sha256.New()
	writeLengthPrefixedInput := func(value []byte) {
		var byteCount [8]byte
		binary.BigEndian.PutUint64(byteCount[:], uint64(len(value)))
		_, _ = digest.Write(byteCount[:])
		_, _ = digest.Write(value)
	}
	writeLengthPrefixedInput([]byte(inputs.SourceFingerprint))
	writeLengthPrefixedInput(inputs.CurrentRenderedStillSHA256)
	writeLengthPrefixedInput(inputs.ImmutableOriginalImageFactsSHA256)
	writeLengthPrefixedInput(inputs.ExtractedPhotoTextSHA256)
	writeLengthPrefixedInput(inputs.LocationEvidenceSHA256)
	writeLengthPrefixedInput([]byte(inputs.HumanReadableInstructions))
	writeLengthPrefixedInput(inputs.StructuredOutputSchemaJSON)
	writeLengthPrefixedInput([]byte(inputs.ModelIdentifier))
	return digest.Sum(nil)
}
