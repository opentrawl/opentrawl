package projection

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

func DecodeText(zdata []byte) (string, error) {
	body, err := Inflate(zdata)
	if err != nil {
		return "", err
	}
	document, err := firstLengthDelimited(body, 2)
	if err != nil {
		return "", fmt.Errorf("document field: %w", err)
	}
	note, err := firstLengthDelimited(document, 3)
	if err != nil {
		return "", fmt.Errorf("note field: %w", err)
	}
	text, err := firstString(note, 2)
	if err != nil {
		return "", fmt.Errorf("text field: %w", err)
	}
	return text, nil
}

func Inflate(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, errors.New("note body is too short")
	}
	switch {
	case data[0] == 0x1f && data[1] == 0x8b:
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(zr)
	case data[0] == 0x78:
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(zr)
	default:
		return nil, errors.New("note body is not gzip or zlib data")
	}
}

func firstString(data []byte, field uint64) (string, error) {
	value, err := firstLengthDelimited(data, field)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func firstLengthDelimited(data []byte, field uint64) ([]byte, error) {
	for i := 0; i < len(data); {
		key, n, err := readVarint(data[i:])
		if err != nil {
			return nil, err
		}
		i += n
		number := key >> 3
		wire := key & 7
		switch wire {
		case 0:
			_, n, err := readVarint(data[i:])
			if err != nil {
				return nil, err
			}
			i += n
		case 1:
			i += 8
		case 2:
			length, n, err := readVarint(data[i:])
			if err != nil {
				return nil, err
			}
			i += n
			end := i + int(length)
			if end < i || end > len(data) {
				return nil, errors.New("length-delimited field exceeds message")
			}
			if number == field {
				return data[i:end], nil
			}
			i = end
		case 5:
			i += 4
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
		if i > len(data) {
			return nil, errors.New("protobuf field exceeds message")
		}
	}
	return nil, fmt.Errorf("field %d not found", field)
}

func readVarint(data []byte) (uint64, int, error) {
	value, n := binary.Uvarint(data)
	if n > 0 {
		return value, n, nil
	}
	if n < 0 {
		return 0, 0, errors.New("varint too long")
	}
	return 0, 0, io.ErrUnexpectedEOF
}
