package crawlkit

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/openclaw/crawlkit/output"
	workerv1 "github.com/openclaw/crawlkit/proto/trawl/worker/v1"
	"google.golang.org/protobuf/proto"
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
	msg, err := proto.Marshal(wireFrame)
	if err != nil {
		return err
	}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(len(msg)))
	if _, err := w.Write(prefix[:n]); err != nil {
		return err
	}
	_, err = w.Write(msg)
	return err
}

func readChildFrame(reader *bufio.Reader) (childFrame, error) {
	size, err := binary.ReadUvarint(reader)
	if err != nil {
		return childFrame{}, err
	}
	if size == 0 {
		return childFrame{}, errors.New("empty child frame")
	}
	if size > uint64(int(^uint(0)>>1)) {
		return childFrame{}, fmt.Errorf("child frame too large: %d bytes", size)
	}
	msg := make([]byte, int(size))
	if _, err := io.ReadFull(reader, msg); err != nil {
		return childFrame{}, err
	}
	var wireFrame workerv1.Frame
	if err := proto.Unmarshal(msg, &wireFrame); err != nil {
		return childFrame{}, fmt.Errorf("decode child frame: %w", err)
	}
	frame, err := childFrameFromProto(&wireFrame)
	if err != nil {
		return childFrame{}, fmt.Errorf("decode child frame: %w", err)
	}
	return frame, nil
}

func childFrameToProto(frame childFrame) (*workerv1.Frame, error) {
	switch frame.kind {
	case childFrameProgress:
		return &workerv1.Frame{
			Kind: &workerv1.Frame_Progress{Progress: &workerv1.Progress{
				Phase:   frame.progress.Phase,
				Done:    frame.progress.Done,
				Total:   frame.progress.Total,
				Message: frame.progress.Message,
			}},
		}, nil
	case childFrameLog:
		return &workerv1.Frame{
			Kind: &workerv1.Frame_Log{Log: &workerv1.Log{Text: frame.logText}},
		}, nil
	case childFrameResult:
		result := &workerv1.Result{Output: frame.output}
		if frame.errorBody != nil {
			result.Error = childErrorToProto(*frame.errorBody)
		}
		return &workerv1.Frame{
			Kind: &workerv1.Frame_Result{Result: result},
		}, nil
	default:
		return nil, fmt.Errorf("unknown child frame kind %d", frame.kind)
	}
}

func childFrameFromProto(frame *workerv1.Frame) (childFrame, error) {
	switch kind := frame.GetKind().(type) {
	case *workerv1.Frame_Progress:
		if kind.Progress == nil {
			return childFrame{}, errors.New("progress frame missing progress")
		}
		return childProgressFrame(Progress{
			Phase:   kind.Progress.GetPhase(),
			Done:    kind.Progress.GetDone(),
			Total:   kind.Progress.GetTotal(),
			Message: kind.Progress.GetMessage(),
		}), nil
	case *workerv1.Frame_Log:
		if kind.Log == nil {
			return childFrame{}, errors.New("log frame missing log")
		}
		return childLogFrame(kind.Log.GetText()), nil
	case *workerv1.Frame_Result:
		if kind.Result == nil {
			return childFrame{}, errors.New("result frame missing result")
		}
		var body *output.ErrorBody
		if kind.Result.Error != nil {
			errorBody := output.ErrorBody{
				Code:    kind.Result.Error.GetCode(),
				Message: kind.Result.Error.GetMessage(),
				Remedy:  kind.Result.Error.GetRemedy(),
			}
			if kind.Result.Error.GetLockPath() != "" {
				errorBody.Fields = map[string]any{"lock_path": kind.Result.Error.GetLockPath()}
			}
			body = &errorBody
		}
		return childResultFrame(kind.Result.GetOutput(), body), nil
	default:
		return childFrame{}, errors.New("child frame missing kind")
	}
}

func childErrorToProto(body output.ErrorBody) *workerv1.Error {
	wireError := &workerv1.Error{
		Code:    body.Code,
		Message: body.Message,
		Remedy:  body.Remedy,
	}
	if lockPath, ok := body.Fields["lock_path"].(string); ok {
		wireError.LockPath = lockPath
	}
	return wireError
}
