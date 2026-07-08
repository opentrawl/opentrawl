package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
)

const (
	walHeaderBytes   = 32
	frameHeaderBytes = 24
	magicBigEndian   = 0x377f0682
	magicLittleCksum = 0x377f0683
)

type walHeader struct {
	pageSize      int
	salt          [8]byte
	checksumOrder binary.ByteOrder
	checksumWords binary.ByteOrder
	checksum      checksumState
}

type checksumState struct {
	first  uint32
	second uint32
}

func CommitOffsetsFile(path string) ([]int64, []byte, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- caller passes a copied WAL path or test fixture.
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	offsets, err := CommitOffsets(data)
	return offsets, data, err
}

func CommitOffsets(data []byte) ([]int64, error) {
	if len(data) == 0 {
		return nil, nil
	}
	header, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	frameBytes := frameHeaderBytes + header.pageSize
	offsets := []int64{}
	sum := header.checksum
	for offset := walHeaderBytes; offset+frameBytes <= len(data); offset += frameBytes {
		frame := data[offset : offset+frameBytes]
		next, commit, ok := header.readFrame(frame, sum)
		if !ok {
			break
		}
		sum = next
		if commit {
			offsets = append(offsets, int64(offset+frameBytes))
		}
	}
	return offsets, nil
}

func parseHeader(data []byte) (walHeader, error) {
	if len(data) < walHeaderBytes {
		return walHeader{}, errors.New("wal header is truncated")
	}
	magic := binary.BigEndian.Uint32(data[0:4])
	preferred, err := checksumOrderForMagic(magic)
	if err != nil {
		return walHeader{}, err
	}
	pageSize := binary.BigEndian.Uint32(data[8:12])
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize == 0 {
		return walHeader{}, errors.New("wal page size is zero")
	}
	order, words, sum, ok := headerChecksum(preferred, data[:walHeaderBytes])
	if !ok {
		return walHeader{}, errors.New("wal header checksum mismatch")
	}
	header := walHeader{
		pageSize:      int(pageSize),
		checksumOrder: order,
		checksumWords: words,
		checksum:      sum,
	}
	copy(header.salt[:], data[16:24])
	return header, nil
}

func (h walHeader) readFrame(frame []byte, previous checksumState) (checksumState, bool, bool) {
	head := frame[:frameHeaderBytes]
	if !bytes.Equal(head[8:16], h.salt[:]) {
		return checksumState{}, false, false
	}
	sum := extendChecksum(h.checksumOrder, head[:8], previous)
	sum = extendChecksum(h.checksumOrder, frame[frameHeaderBytes:], sum)
	if !h.hasChecksum(head[16:24], sum) {
		return checksumState{}, false, false
	}
	return sum, binary.BigEndian.Uint32(head[4:8]) != 0, true
}

func (h walHeader) hasChecksum(data []byte, sum checksumState) bool {
	return sum.first == h.checksumWords.Uint32(data[0:4]) &&
		sum.second == h.checksumWords.Uint32(data[4:8])
}

func checksumOrderForMagic(magic uint32) (binary.ByteOrder, error) {
	switch magic {
	case magicBigEndian:
		return binary.BigEndian, nil
	case magicLittleCksum:
		return binary.LittleEndian, nil
	default:
		return nil, errors.New("not a sqlite wal file")
	}
}

func headerChecksum(preferred binary.ByteOrder, header []byte) (binary.ByteOrder, binary.ByteOrder, checksumState, bool) {
	for _, order := range preferredOrders(preferred) {
		sum := extendChecksum(order, header[:24], checksumState{})
		words, ok := checksumWordOrder(header[24:32], sum)
		if ok {
			return order, words, sum, true
		}
	}
	return nil, nil, checksumState{}, false
}

func preferredOrders(first binary.ByteOrder) []binary.ByteOrder {
	if first == binary.BigEndian {
		return []binary.ByteOrder{binary.BigEndian, binary.LittleEndian}
	}
	return []binary.ByteOrder{binary.LittleEndian, binary.BigEndian}
}

func checksumWordOrder(data []byte, sum checksumState) (binary.ByteOrder, bool) {
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		if sum.first == order.Uint32(data[0:4]) && sum.second == order.Uint32(data[4:8]) {
			return order, true
		}
	}
	return nil, false
}

func extendChecksum(order binary.ByteOrder, data []byte, sum checksumState) checksumState {
	for i := 0; i+7 < len(data); i += 8 {
		sum.first += order.Uint32(data[i:i+4]) + sum.second
		sum.second += order.Uint32(data[i+4:i+8]) + sum.first
	}
	return sum
}
