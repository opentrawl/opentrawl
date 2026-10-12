// Package photospipeline connects the five Photos steps to the archive and
// runs them. A step is a typed function with no archive access; a binding
// selects the step's pending work from the archive and stores its output.
package photospipeline

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/proto"
)

// PhotosStepRunsOnOneInput takes one photo, spot or request and returns one
// typed result. It has no archive access.
type PhotosStepRunsOnOneInput[StepInput, StepOutput proto.Message] interface {
	PhotosStepName() photosstep.PhotosStepName
	RunPhotosStepOnOneInput(ctx context.Context, stepInput StepInput) (StepOutput, error)
}

// PhotosStepArchiveBinding connects a step to the archive: what is pending,
// and how its output is stored.
type PhotosStepArchiveBinding[StepInput, StepOutput proto.Message] interface {
	SelectPendingPhotosStepInputsNewestFirst(
		ctx context.Context,
		photosArchive *photosarchive.PhotosArchive,
		workSelection PhotosStepWorkSelection,
	) ([]StepInput, error)
	StorePhotosStepOutput(
		ctx context.Context,
		photosArchive *photosarchive.PhotosArchive,
		stepInput StepInput,
		stepOutput StepOutput,
	) error
}

// PhotosStepWorkSelection narrows a step's work list for one run.
type PhotosStepWorkSelection struct {
	OnlyApplePhotoUUID          string
	Limit                       int
	IncludeStaleCards           bool
	RetryPermanentlyFailedWork  bool
	SelectionTime               time.Time
	ReadWithoutCreatingSnapshot bool
}

func (workSelection PhotosStepWorkSelection) selectsAllPendingWork() bool {
	return workSelection.OnlyApplePhotoUUID == "" && workSelection.Limit == photosarchive.NoWorkListLimit
}

// errPhotosStepNotImplemented pauses a step for the run, with no per-photo
// rows, until the step is built.
var errPhotosStepNotImplemented = errors.New("not implemented")

// PhotosStepCommandName is the name a person types: "read_apple_library" for
// PHOTOS_STEP_NAME_READ_APPLE_LIBRARY.
func PhotosStepCommandName(photosStepName photosstep.PhotosStepName) string {
	return strings.ToLower(strings.TrimPrefix(photosStepName.String(), "PHOTOS_STEP_NAME_"))
}

func ParsePhotosStepCommandName(photosStepCommandName string) (photosstep.PhotosStepName, bool) {
	photosStepName, ok := photosstep.PhotosStepName_value["PHOTOS_STEP_NAME_"+strings.ToUpper(strings.TrimSpace(photosStepCommandName))]
	if !ok || photosStepName == int32(photosstep.PhotosStepName_PHOTOS_STEP_NAME_UNSPECIFIED) {
		return photosstep.PhotosStepName_PHOTOS_STEP_NAME_UNSPECIFIED, false
	}
	return photosstep.PhotosStepName(photosStepName), true
}

func AllPhotosStepNamesInOrder() []photosstep.PhotosStepName {
	return []photosstep.PhotosStepName{
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_LOOK_UP_PLACES_NEAR_SPOT,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_CARD_MODEL_INPUT,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD,
	}
}
