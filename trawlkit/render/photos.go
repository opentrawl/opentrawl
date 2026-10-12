package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func photosStepCommandName(step photosstep.PhotosStepName) string {
	return strings.ToLower(strings.TrimPrefix(step.String(), "PHOTOS_STEP_NAME_"))
}

func PhotosRunNeedsStep() CommandFailure { return commandIsWrong("Name one Photos step.") }
func PhotosStepNameIsUnknown(name string) CommandFailure {
	return commandIsWrong(quotedForSentence(name) + " is not a Photos step.")
}
func PhotosRunLimitMustBePositive() CommandFailure {
	return commandIsWrong("--limit must be at least 1.")
}
func PhotosRecardNeedsCardStep() CommandFailure {
	return commandIsWrong("--recard applies only to write_photo_card.")
}
func PhotosCardRunNeedsSelection() CommandFailure {
	return commandIsWrong("Card runs need --photo or --limit.")
}
func PhotosStepsNotBuilt(steps []photosstep.PhotosStepName) CommandFailure {
	failure := archiveOrOperationFailed(photosStepsNotBuiltSentence(steps))
	failure.photosStepsNotBuilt = append([]photosstep.PhotosStepName(nil), steps...)
	return failure
}
func photosStepsNotBuiltSentence(steps []photosstep.PhotosStepName) string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, photosStepCommandName(step))
	}
	if len(names) == 1 {
		return "The Photos step " + names[0] + " is not built yet."
	}
	return "These Photos steps are not built yet: " + strings.Join(names, ", ") + "."
}

func writePhotosStepRunResponse(writer io.Writer, response *photosstep.PhotosStepRunResponse) error {
	if path := response.GetExactInputAndOutputFilePath(); path != "" {
		return writeLine(writer, "The exact input and output is too large to print. It is in "+path+".")
	}
	for _, item := range response.GetStepItems() {
		var input, output proto.Message
		switch typedInput := item.GetStepInput().(type) {
		case *photosstep.PhotosStepRunItem_ReadAppleLibraryRequest:
			input = typedInput.ReadAppleLibraryRequest
		case *photosstep.PhotosStepRunItem_FetchPictureRequest:
			input = typedInput.FetchPictureRequest
		case *photosstep.PhotosStepRunItem_LookUpPlacesNearSpotRequest:
			input = typedInput.LookUpPlacesNearSpotRequest
		case *photosstep.PhotosStepRunItem_WriteCardModelInputRequest:
			input = typedInput.WriteCardModelInputRequest
		case *photosstep.PhotosStepRunItem_WritePhotoCardRequest:
			input = typedInput.WritePhotoCardRequest
		}
		switch typedOutput := item.GetStepOutput().(type) {
		case *photosstep.PhotosStepRunItem_AppleLibrarySnapshot:
			output = typedOutput.AppleLibrarySnapshot
		case *photosstep.PhotosStepRunItem_PictureFetchResult:
			output = typedOutput.PictureFetchResult
		case *photosstep.PhotosStepRunItem_NearbyPlacesLookupResult:
			output = typedOutput.NearbyPlacesLookupResult
		case *photosstep.PhotosStepRunItem_PhotoCardModelInput:
			output = typedOutput.PhotoCardModelInput
		case *photosstep.PhotosStepRunItem_PhotoCard:
			output = typedOutput.PhotoCard
		}
		if err := writeExactPhotosStepMessage(writer, "input", input); err != nil {
			return err
		}
		if err := writeExactPhotosStepMessage(writer, "output", output); err != nil {
			return err
		}
	}
	switch response.GetStopReason() {
	case photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_STEP_NOT_IMPLEMENTED:
		return writeLine(writer, "output: none; "+photosStepCommandName(response.GetPhotosStepName())+" is not built yet.")
	case photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_NOTHING_PENDING:
		if response.GetCompletedItemCount() == 0 {
			return writeLine(writer, "Nothing is waiting for "+photosStepCommandName(response.GetPhotosStepName())+".")
		}
	}
	return nil
}

func writeExactPhotosStepMessage(writer io.Writer, role string, message proto.Message) error {
	if message == nil {
		return nil
	}
	serialized, err := (prototext.MarshalOptions{Multiline: true, Indent: "  "}).Marshal(message)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "%s %s {\n", role, message.ProtoReflect().Descriptor().Name()); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(serialized), "\n"), "\n") {
		if _, err := fmt.Fprintln(writer, "  "+line); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(writer, "}")
	return err
}

func writePhotosPipelineStatus(writer io.Writer, pipeline *photosstep.PhotosPipelineStatus) error {
	if pipeline == nil {
		return nil
	}
	anyWaitingItemCountIsKnown := false
	for _, step := range pipeline.GetStepsInExecutionOrder() {
		anyWaitingItemCountIsKnown = anyWaitingItemCountIsKnown || step.WaitingItemCount != nil
	}
	rows := make([][]string, 0, len(pipeline.GetStepsInExecutionOrder()))
	for _, step := range pipeline.GetStepsInExecutionOrder() {
		waiting := ""
		if step.WaitingItemCount != nil {
			waiting = FormatInteger(int64(step.GetWaitingItemCount()))
		}
		reason := "built"
		switch {
		case !step.GetStepIsBuilt():
			reason = "not built yet"
		case step.GetLastRunStopReason() == photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_CARD_RUNS_NOT_APPROVED:
			reason = "card runs not approved"
		case step.GetWaitingOnEarlierSteps():
			reason = "waiting on earlier steps"
		}
		row := []string{photosStepCommandName(step.GetPhotosStepName())}
		if anyWaitingItemCountIsKnown {
			row = append(row, waiting)
		}
		rows = append(rows, append(row, reason))
	}
	columns := []TableColumn{{Header: "step"}}
	if anyWaitingItemCountIsKnown {
		columns = append(columns, TableColumn{Header: "waiting"})
	}
	columns = append(columns, TableColumn{Header: "status", Wrap: true})
	return writeTable(writer, columns, rows, len(columns)-1)
}

// PreparePhotosStepRunResponseForTransport retains large exact output in the
// same library that renders it. Read-only runs bypass the subprocess transport.
func PreparePhotosStepRunResponseForTransport(response *photosstep.PhotosStepRunResponse, archivePath string) (*photosstep.PhotosStepRunResponse, error) {
	if proto.Size(response) <= 8<<20 {
		return response, nil
	}
	directory := filepath.Join(filepath.Dir(archivePath), "step-runs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, photosStepCommandName(response.GetPhotosStepName())+".txtpb")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := writePhotosStepRunResponse(file, response); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return &photosstep.PhotosStepRunResponse{
		PhotosStepName: response.GetPhotosStepName(), CompletedItemCount: response.GetCompletedItemCount(), StopReason: response.GetStopReason(),
		ExactInputAndOutputFilePath: path,
	}, nil
}
