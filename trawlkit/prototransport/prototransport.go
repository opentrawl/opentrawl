// Package prototransport frames protobuf messages on streams shared by
// independently built processes.
package prototransport

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

const maxFrameBytes = 16 << 20

func WriteDelimited(w io.Writer, message proto.Message) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return errors.New("empty protobuf frame")
	}
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("protobuf frame is %d bytes; maximum is %d", len(payload), maxFrameBytes)
	}
	var prefix [binary.MaxVarintLen64]byte
	prefixLength := binary.PutUvarint(prefix[:], uint64(len(payload)))
	if _, err := w.Write(prefix[:prefixLength]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func ReadDelimited(reader *bufio.Reader, message proto.Message) error {
	size, err := binary.ReadUvarint(reader)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		return fmt.Errorf("read protobuf frame length: %w", err)
	}
	if size == 0 {
		return errors.New("empty protobuf frame")
	}
	if size > maxFrameBytes {
		return fmt.Errorf("protobuf frame is %d bytes; maximum is %d", size, maxFrameBytes)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("read protobuf frame body: %w", err)
	}
	if err := proto.Unmarshal(payload, message); err != nil {
		return fmt.Errorf("decode protobuf frame: %w", err)
	}
	return nil
}
