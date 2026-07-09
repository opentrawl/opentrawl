package projection

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// This package decodes Apple Notes bodies by hand rather than pulling in a
// protobuf runtime: the wire format is small and fixed, and the repo has no
// generated code for it. fields walks one message level; message accessors
// pull singular or repeated fields out of the result.

var errShortMessage = errors.New("protobuf field exceeds message")

type wireField struct {
	number uint64
	wire   uint64
	varint uint64 // wire types 0, 1 and 5
	bytes  []byte // wire type 2
}

// fields decodes every top-level field of a single protobuf message. It does
// not recurse; callers pull nested messages out by field number and decode
// them in turn.
func fields(data []byte) ([]wireField, error) {
	var out []wireField
	for i := 0; i < len(data); {
		key, n := binary.Uvarint(data[i:])
		if n <= 0 {
			return nil, errShortMessage
		}
		i += n
		f := wireField{number: key >> 3, wire: key & 7}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(data[i:])
			if n <= 0 {
				return nil, errShortMessage
			}
			f.varint = v
			i += n
		case 1:
			if i+8 > len(data) {
				return nil, errShortMessage
			}
			f.varint = binary.LittleEndian.Uint64(data[i:])
			i += 8
		case 2:
			length, n := binary.Uvarint(data[i:])
			if n <= 0 {
				return nil, errShortMessage
			}
			i += n
			end := i + int(length)
			if end < i || end > len(data) {
				return nil, errShortMessage
			}
			f.bytes = data[i:end]
			i = end
		case 5:
			if i+4 > len(data) {
				return nil, errShortMessage
			}
			f.varint = uint64(binary.LittleEndian.Uint32(data[i:]))
			i += 4
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

// message parses data and exposes typed accessors over the decoded fields.
type message []wireField

func parse(data []byte) (message, error) {
	fs, err := fields(data)
	if err != nil {
		return nil, err
	}
	return message(fs), nil
}

// varint returns the last value for a singular scalar field (proto2 keeps the
// last occurrence). ok reports whether the field was present.
func (m message) varint(number uint64) (uint64, bool) {
	var value uint64
	found := false
	for _, f := range m {
		if f.number == number && (f.wire == 0 || f.wire == 1 || f.wire == 5) {
			value = f.varint
			found = true
		}
	}
	return value, found
}

// int32 reads a signed int32 field. Negative proto2 int32s are encoded as
// 10-byte two's-complement varints, so a plain uint64→int32 cast recovers the
// sign (e.g. style_type default -1).
func (m message) int32(number uint64, fallback int32) int32 {
	if v, ok := m.varint(number); ok {
		return int32(v)
	}
	return fallback
}

// bytes returns the last length-delimited value for a singular field.
func (m message) bytes(number uint64) ([]byte, bool) {
	var value []byte
	found := false
	for _, f := range m {
		if f.number == number && f.wire == 2 {
			value = f.bytes
			found = true
		}
	}
	return value, found
}

func (m message) str(number uint64) (string, bool) {
	if b, ok := m.bytes(number); ok {
		return string(b), true
	}
	return "", false
}

// repeated returns every length-delimited value for a repeated field, in
// wire order.
func (m message) repeated(number uint64) [][]byte {
	var out [][]byte
	for _, f := range m {
		if f.number == number && f.wire == 2 {
			out = append(out, f.bytes)
		}
	}
	return out
}

func (m message) repeatedStr(number uint64) []string {
	raw := m.repeated(number)
	out := make([]string, 0, len(raw))
	for _, b := range raw {
		out = append(out, string(b))
	}
	return out
}

// child parses a nested singular message field. present reports whether the
// field existed at all.
func (m message) child(number uint64) (message, bool, error) {
	b, ok := m.bytes(number)
	if !ok {
		return nil, false, nil
	}
	child, err := parse(b)
	if err != nil {
		return nil, true, err
	}
	return child, true, nil
}
