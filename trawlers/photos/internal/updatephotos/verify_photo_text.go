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
	"google.golang.org/protobuf/proto"
)

type photoTextVerificationDerivationInputs struct {
	CurrentRenderedStillSHA256 []byte
	ExtractedPhotoTextSHA256   []byte
	HumanReadableInstructions  string
	StructuredOutputSchemaJSON []byte
	ModelIdentifier            string
}

func (worker *photoAssetWorker) verifyPhotoText(ctx context.Context, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence, extractedPhotoText *cardwire.PhotoOpticalCharacterRecognition) (*cardwire.PhotoOpticalCharacterRecognition, error) {
	runner := worker.runner
	instructions, err := photocard.BuildPhotoTextVerificationInstructions(extractedPhotoText)
	if err != nil {
		return nil, err
	}
	structuredOutputSchemaJSON, err := photocard.PhotoTextVerificationStructuredOutputSchemaJSON()
	if err != nil {
		return nil, err
	}
	structuredOutputSchema, err := luna.NewStructuredOutputSchema(structuredOutputSchemaJSON)
	if err != nil {
		return nil, err
	}
	extractedPhotoTextBytes, err := proto.Marshal(extractedPhotoText)
	if err != nil {
		return nil, err
	}
	extractedPhotoTextDigest := sha256.Sum256(extractedPhotoTextBytes)
	inputSHA256 := photoTextVerificationDerivationInputs{
		CurrentRenderedStillSHA256: mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(),
		ExtractedPhotoTextSHA256:   extractedPhotoTextDigest[:],
		HumanReadableInstructions:  instructions,
		StructuredOutputSchemaJSON: structuredOutputSchemaJSON,
		ModelIdentifier:            luna.ModelGPT56Luna,
	}.SHA256()
	if err := archive.RetainPhotoTextVerificationRequest(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, instructions); err != nil {
		return nil, err
	}
	if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextVerification, archive.PhotoModelGenerationStateRequestRetained, "", "", "", time.Now()); err != nil {
		return nil, err
	}
	retained, found, err := archive.LoadRetainedPhotoTextVerification(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	if err != nil {
		return nil, err
	}
	response := retained.ResponseBody
	if !found || len(response) == 0 || retained.ResponseRejected {
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
			TransmissionStarted: func(threadIdentifier string) error {
				return archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextVerification, archive.PhotoModelGenerationStateTransmissionStarted, threadIdentifier, "", "", time.Now())
			},
			ResponseReceived: func(received luna.GenerationResult) error {
				return archive.RetainPhotoTextVerificationResponse(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, received.ThreadID, received.TurnID, received.RawStructuredOutputJSON, photoModelGenerationUsage(received), time.Now())
			},
		})
		if err != nil {
			if retainErr := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextVerification, archive.PhotoModelGenerationStateFailed, "", "", err.Error(), time.Now()); retainErr != nil {
				return nil, errors.Join(err, retainErr)
			}
			return nil, &AssetDeferredError{Reason: "Luna photo text verification remains retryable"}
		}
		response = generation.RawStructuredOutputJSON
		retained.ThreadIdentifier = generation.ThreadID
		retained.TurnIdentifier = generation.TurnID
	}
	verification := new(cardwire.PhotoOpticalCharacterRecognitionVerification)
	if err := protojson.Unmarshal(response, verification); err != nil {
		return nil, worker.deferRejectedPhotoTextVerificationResult(ctx, asset.AssetID, inputSHA256, retained.ThreadIdentifier, retained.TurnIdentifier, errors.New("Luna photo text verification response did not match the generated Protobuf schema"))
	}
	verifiedPhotoText, err := photocard.ApplyPhotoTextVerification(extractedPhotoText, verification)
	if err != nil {
		return nil, worker.deferRejectedPhotoTextVerificationResult(ctx, asset.AssetID, inputSHA256, retained.ThreadIdentifier, retained.TurnIdentifier, err)
	}
	if err := archive.RetainPhotoModelGenerationOperationStage(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, archive.PhotoModelGenerationPhaseTextVerification, archive.PhotoModelGenerationStateSucceeded, retained.ThreadIdentifier, retained.TurnIdentifier, "", time.Now()); err != nil {
		return nil, err
	}
	return verifiedPhotoText, nil
}

func (worker *photoAssetWorker) deferRejectedPhotoTextVerificationResult(ctx context.Context, assetID archive.PhotoAssetID, inputSHA256 []byte, threadIdentifier, turnIdentifier string, contractViolation error) error {
	if err := archive.RejectRetainedPhotoTextVerificationResponse(ctx, worker.runner.options.OpenedArchiveStore, assetID, inputSHA256, threadIdentifier, turnIdentifier, contractViolation.Error(), time.Now()); err != nil {
		return errors.Join(contractViolation, err)
	}
	return &AssetDeferredError{Reason: contractViolation.Error()}
}

func (inputs photoTextVerificationDerivationInputs) SHA256() []byte {
	digest := sha256.New()
	writeLengthPrefixedInput := func(value []byte) {
		var byteCount [8]byte
		binary.BigEndian.PutUint64(byteCount[:], uint64(len(value)))
		_, _ = digest.Write(byteCount[:])
		_, _ = digest.Write(value)
	}
	writeLengthPrefixedInput(inputs.CurrentRenderedStillSHA256)
	writeLengthPrefixedInput(inputs.ExtractedPhotoTextSHA256)
	writeLengthPrefixedInput([]byte(inputs.HumanReadableInstructions))
	writeLengthPrefixedInput(inputs.StructuredOutputSchemaJSON)
	writeLengthPrefixedInput([]byte(inputs.ModelIdentifier))
	return digest.Sum(nil)
}
