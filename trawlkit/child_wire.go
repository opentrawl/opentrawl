package trawlkit

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	worker "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

var childFrameWriteMu sync.Mutex

func decodeChildFrames(stdout io.Reader, frames chan<- childFrame, errs chan<- error) {
	defer close(frames)
	reader := bufio.NewReader(stdout)
	for {
		frame, err := readChildFrame(reader)
		if err != nil {
			errs <- err
			return
		}
		frames <- frame
	}
}

func writeChildFrame(w io.Writer, frame childFrame) error {
	childFrameWriteMu.Lock()
	defer childFrameWriteMu.Unlock()
	wireFrame, err := childFrameToProto(frame)
	if err != nil {
		return err
	}
	return prototransport.WriteDelimited(w, wireFrame)
}

func readChildFrame(reader *bufio.Reader) (childFrame, error) {
	var wireFrame worker.Frame
	if err := prototransport.ReadDelimited(reader, &wireFrame); err != nil {
		if errors.Is(err, io.EOF) {
			return childFrame{}, err
		}
		return childFrame{}, fmt.Errorf("decode child frame: %w", err)
	}
	frame, err := childFrameFromProto(&wireFrame)
	if err != nil {
		return childFrame{}, fmt.Errorf("decode child frame: %w", err)
	}
	return frame, nil
}

func childFrameToProto(frame childFrame) (*worker.Frame, error) {
	switch frame.kind {
	case childFrameProgress:
		return &worker.Frame{
			Kind: &worker.Frame_Progress{Progress: &worker.Progress{
				Phase:   frame.progress.Phase,
				Done:    frame.progress.Done,
				Total:   frame.progress.Total,
				Message: frame.progress.Message,
			}},
		}, nil
	case childFrameLog:
		return &worker.Frame{
			Kind: &worker.Frame_Log{Log: &worker.Log{Text: frame.logText}},
		}, nil
	case childFrameResult:
		result := &worker.Result{}
		if frame.syncReport != nil {
			result.Success = &worker.Result_Sync{Sync: frame.syncReport}
		} else if frame.trawlerCommandResponse != nil {
			result.Success = &worker.Result_TrawlerCommandResponse{
				TrawlerCommandResponse: frame.trawlerCommandResponse,
			}
		}
		if frame.errorDescription != nil {
			result.Error = childErrorDescriptionToProto(*frame.errorDescription)
		}
		return &worker.Frame{
			Kind: &worker.Frame_Result{Result: result},
		}, nil
	default:
		return nil, fmt.Errorf("unknown child frame kind %d", frame.kind)
	}
}

func childFrameFromProto(frame *worker.Frame) (childFrame, error) {
	switch kind := frame.GetKind().(type) {
	case *worker.Frame_Progress:
		if kind.Progress == nil {
			return childFrame{}, errors.New("progress frame missing progress")
		}
		return childProgressFrame(Progress{
			Phase:   kind.Progress.GetPhase(),
			Done:    kind.Progress.GetDone(),
			Total:   kind.Progress.GetTotal(),
			Message: kind.Progress.GetMessage(),
		}), nil
	case *worker.Frame_Log:
		if kind.Log == nil {
			return childFrame{}, errors.New("log frame missing log")
		}
		return childLogFrame(kind.Log.GetText()), nil
	case *worker.Frame_Result:
		if kind.Result == nil {
			return childFrame{}, errors.New("result frame missing result")
		}
		var errorDescription *output.ErrorDescription
		if kind.Result.Error != nil {
			description := output.ErrorDescription{
				Code:     kind.Result.Error.GetCode(),
				Message:  kind.Result.Error.GetMessage(),
				LockPath: kind.Result.Error.GetLockPath(),
			}
			errorDescription = &description
		}
		if errorDescription != nil && kind.Result.GetSuccess() != nil {
			return childFrame{}, errors.New("result frame combined an error with a success result")
		}
		switch success := kind.Result.GetSuccess().(type) {
		case *worker.Result_Sync:
			if success.Sync == nil {
				return childFrame{}, errors.New("result frame missing sync result")
			}
			return childResultFrame(nil, success.Sync, errorDescription), nil
		case *worker.Result_TrawlerCommandResponse:
			if success.TrawlerCommandResponse == nil {
				return childFrame{}, errors.New("result frame missing trawler command response")
			}
			return childResultFrame(success.TrawlerCommandResponse, nil, errorDescription), nil
		case nil:
			if errorDescription == nil {
				return childFrame{}, errors.New("result frame missing success result")
			}
		default:
			return childFrame{}, errors.New("result frame has unknown success result")
		}
		return childResultFrame(nil, nil, errorDescription), nil
	default:
		return childFrame{}, errors.New("child frame missing kind")
	}
}

func childErrorDescriptionToProto(description output.ErrorDescription) *worker.Error {
	return &worker.Error{
		Code:     description.Code,
		Message:  description.Message,
		LockPath: description.LockPath,
	}
}
