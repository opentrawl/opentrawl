package crawlkit

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
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
	frame.SchemaVersion = 1
	if frame.Type == "" {
		frame.Type = "result"
	}
	msg, err := json.Marshal(frame)
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
	var frame childFrame
	dec := json.NewDecoder(bytes.NewReader(msg))
	dec.UseNumber()
	if err := dec.Decode(&frame); err != nil {
		return childFrame{}, fmt.Errorf("decode child frame: %w", err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			err = errors.New("extra json value")
		}
		return childFrame{}, fmt.Errorf("decode child frame: %w", err)
	}
	return frame, nil
}
