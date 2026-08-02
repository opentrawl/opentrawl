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
	"google.golang.org/protobuf/encoding/protojson"
)

type photoTextDerivationInputs struct {
	CurrentRenderedStillSHA256 []byte
	HumanReadableInstructions  string
	StructuredOutputSchemaJSON []byte
	ModelIdentifier            string
}

func (worker *photoAssetWorker) extractPhotoText(ctx context.Context, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence) (*cardwire.PhotoOpticalCharacterRecognition, error) {
	runner := worker.runner
	instructions := photocard.BuildPhotoTextExtractionInstructions()
	structuredOutputSchemaJSON, err := photocard.PhotoTextStructuredOutputSchemaJSON()
	if err != nil {
		return nil, err
	}
	structuredOutputSchema, err := luna.NewStructuredOutputSchema(structuredOutputSchemaJSON)
	if err != nil {
		return nil, err
	}
	inputSHA256 := photoTextDerivationInputs{
		CurrentRenderedStillSHA256: mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(),
		HumanReadableInstructions:  instructions,
		StructuredOutputSchemaJSON: structuredOutputSchemaJSON,
		ModelIdentifier:            luna.ModelGPT56Luna,
	}.SHA256()
	if err := archive.RetainPhotoTextExtractionRequest(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, instructions); err != nil {
		return nil, err
	}
	if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, archive.PhotoModelGenerationStateRequestRetained, "", "", "", time.Now()); err != nil {
		return nil, err
	}
	retained, found, err := archive.LoadRetainedPhotoTextExtraction(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	if err != nil {
		return nil, err
	}
	response := retained.ResponseBody
	if !found || len(response) == 0 || retained.ResponseRejected {
		retainedOperation, _, err := archive.LoadRetainedPhotoModelGenerationOperation(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction)
		if err != nil {
			return nil, err
		}
		client, err := worker.ensureLunaClient(ctx)
		if err != nil {
			return nil, err
		}
		imageBytes, err := mediaEvidence.CurrentRenderedStill.Read()
		if err != nil {
			return nil, err
		}
		generation, err := client.Generate(ctx, luna.GenerationRequest{
			Instructions: instructions, Image: imageBytes, ImageMediaType: lunaImageMediaType(mediaEvidence.CurrentRenderedStill.Outcome.GetUniformTypeIdentifier()), OutputSchema: structuredOutputSchema,
			RetainedThreadIdentifier: retainedLunaThreadIdentifier(retainedOperation), RetainedTurnIdentifier: retainedLunaTurnIdentifier(retainedOperation),
			TransmissionStarted: func(threadIdentifier string) error {
				return archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, archive.PhotoModelGenerationStateTransmissionStarted, threadIdentifier, "", "", time.Now())
			},
			TurnStarted: func(threadIdentifier, turnIdentifier string) error {
				return archive.RetainPhotoModelGenerationTurnIdentifier(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, threadIdentifier, turnIdentifier, time.Now())
			},
			ResponseReceived: func(received luna.GenerationResult) error {
				return archive.RetainPhotoTextExtractionResponse(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, received.ThreadID, received.TurnID, received.RawStructuredOutputJSON, photoModelGenerationUsage(received), time.Now())
			},
		})
		if err != nil {
			if !errors.Is(err, luna.ErrGenerationOutcomePending) {
				if retainErr := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, archive.PhotoModelGenerationStateFailed, generation.ThreadID, generation.TurnID, err.Error(), time.Now()); retainErr != nil {
					return nil, errors.Join(err, retainErr)
				}
			}
			return nil, &AssetDeferredError{Reason: "Luna photo text extraction remains retryable"}
		}
		response = generation.RawStructuredOutputJSON
		retained.ThreadIdentifier = generation.ThreadID
		retained.TurnIdentifier = generation.TurnID
	}
	extractedPhotoText := new(cardwire.PhotoOpticalCharacterRecognition)
	if err := protojson.Unmarshal(response, extractedPhotoText); err != nil {
		return nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, retained.ThreadIdentifier, retained.TurnIdentifier, errors.New("Luna photo text response did not match the generated Protobuf schema"))
	}
	if err := photocard.ValidateExtractedPhotoText(extractedPhotoText); err != nil {
		return nil, worker.deferRejectedPhotoModelResult(ctx, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, retained.ThreadIdentifier, retained.TurnIdentifier, err)
	}
	if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextExtraction, archive.PhotoModelGenerationStateSucceeded, retained.ThreadIdentifier, retained.TurnIdentifier, "", time.Now()); err != nil {
		return nil, err
	}
	return extractedPhotoText, nil
}

func (inputs photoTextDerivationInputs) SHA256() []byte {
	digest := sha256.New()
	writeLengthPrefixedInput := func(value []byte) {
		var byteCount [8]byte
		binary.BigEndian.PutUint64(byteCount[:], uint64(len(value)))
		_, _ = digest.Write(byteCount[:])
		_, _ = digest.Write(value)
	}
	writeLengthPrefixedInput(inputs.CurrentRenderedStillSHA256)
	writeLengthPrefixedInput([]byte(inputs.HumanReadableInstructions))
	writeLengthPrefixedInput(inputs.StructuredOutputSchemaJSON)
	writeLengthPrefixedInput([]byte(inputs.ModelIdentifier))
	return digest.Sum(nil)
}
