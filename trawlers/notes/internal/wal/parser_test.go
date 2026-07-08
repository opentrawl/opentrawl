package wal

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

type walFixture struct {
	magic         uint32
	pageSize      int
	pageSizeField uint32
	checksumOrder binary.ByteOrder
	checksumWords binary.ByteOrder
	frames        []walFrameFixture
}

type walFrameFixture struct {
	pageNumber      uint32
	commitPages     uint32
	fill            byte
	corruptSalt     bool
	corruptChecksum bool
}

func TestCommitOffsetsHeaderValidation(t *testing.T) {
	valid := buildWAL(t, walFixture{})
	badChecksum := append([]byte{}, valid...)
	badChecksum[31] ^= 0xff

	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{
			name: "empty wal",
		},
		{
			name:    "truncated header",
			data:    make([]byte, walHeaderBytes-1),
			wantErr: "wal header is truncated",
		},
		{
			name:    "unknown magic",
			data:    make([]byte, walHeaderBytes),
			wantErr: "not a sqlite wal file",
		},
		{
			name:    "zero page size",
			data:    rawHeader(magicBigEndian, 0),
			wantErr: "wal page size is zero",
		},
		{
			name:    "header checksum mismatch",
			data:    badChecksum,
			wantErr: "wal header checksum mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offsets, err := CommitOffsets(tt.data)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(offsets) != 0 {
					t.Fatalf("offsets = %v, want none", offsets)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCommitOffsetsCommitBoundaries(t *testing.T) {
	const pageSize = 512
	frameBytes := int64(frameHeaderBytes + pageSize)
	data := buildWAL(t, walFixture{
		pageSize: pageSize,
		frames: []walFrameFixture{
			{pageNumber: 1, fill: 0x11},
			{pageNumber: 2, commitPages: 2, fill: 0x22},
			{pageNumber: 1, fill: 0x33},
			{pageNumber: 2, commitPages: 2, fill: 0x44},
		},
	})
	want := []int64{
		int64(walHeaderBytes) + 2*frameBytes,
		int64(walHeaderBytes) + 4*frameBytes,
	}

	offsets, err := CommitOffsets(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %v, want %v", offsets, want)
	}
}

func TestCommitOffsetsAcceptsChecksumVariants(t *testing.T) {
	tests := []struct {
		name          string
		magic         uint32
		pageSize      int
		pageSizeField uint32
		checksumOrder binary.ByteOrder
		checksumWords binary.ByteOrder
	}{
		{
			name:  "big endian magic",
			magic: magicBigEndian,
		},
		{
			name:  "little endian checksum magic",
			magic: magicLittleCksum,
		},
		{
			name:          "alternate stored checksum words",
			magic:         magicBigEndian,
			checksumWords: binary.LittleEndian,
		},
		{
			name:          "page size sentinel",
			magic:         magicBigEndian,
			pageSize:      65536,
			pageSizeField: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageSize := tt.pageSize
			if pageSize == 0 {
				pageSize = 512
			}
			data := buildWAL(t, walFixture{
				magic:         tt.magic,
				pageSize:      pageSize,
				pageSizeField: tt.pageSizeField,
				checksumOrder: tt.checksumOrder,
				checksumWords: tt.checksumWords,
				frames: []walFrameFixture{
					{pageNumber: 1, commitPages: 1, fill: 0x55},
				},
			})
			want := []int64{int64(walHeaderBytes + frameHeaderBytes + pageSize)}

			offsets, err := CommitOffsets(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(offsets, want) {
				t.Fatalf("offsets = %v, want %v", offsets, want)
			}
		})
	}
}

func TestCommitOffsetsStopsAtInvalidFrame(t *testing.T) {
	const pageSize = 512
	firstCommit := int64(walHeaderBytes + frameHeaderBytes + pageSize)

	tests := []struct {
		name string
		data []byte
		want []int64
	}{
		{
			name: "salt mismatch",
			data: buildWAL(t, walFixture{
				pageSize: pageSize,
				frames: []walFrameFixture{
					{pageNumber: 1, commitPages: 1, fill: 0x11},
					{pageNumber: 1, commitPages: 1, fill: 0x22, corruptSalt: true},
				},
			}),
			want: []int64{firstCommit},
		},
		{
			name: "frame checksum mismatch",
			data: buildWAL(t, walFixture{
				pageSize: pageSize,
				frames: []walFrameFixture{
					{pageNumber: 1, commitPages: 1, fill: 0x11},
					{pageNumber: 1, commitPages: 1, fill: 0x22, corruptChecksum: true},
				},
			}),
			want: []int64{firstCommit},
		},
		{
			name: "trailing partial frame",
			data: append(buildWAL(t, walFixture{
				pageSize: pageSize,
				frames: []walFrameFixture{
					{pageNumber: 1, commitPages: 1, fill: 0x11},
				},
			}), make([]byte, frameHeaderBytes+10)...),
			want: []int64{firstCommit},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offsets, err := CommitOffsets(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(offsets, tt.want) {
				t.Fatalf("offsets = %v, want %v", offsets, tt.want)
			}
		})
	}
}

func TestCommitOffsetsFileMissing(t *testing.T) {
	offsets, data, err := CommitOffsetsFile(t.TempDir() + "/missing-wal")
	if err != nil {
		t.Fatal(err)
	}
	if offsets != nil || data != nil {
		t.Fatalf("missing wal returned offsets=%v data=%v", offsets, data)
	}
}

func rawHeader(magic, pageSize uint32) []byte {
	header := make([]byte, walHeaderBytes)
	binary.BigEndian.PutUint32(header[0:4], magic)
	binary.BigEndian.PutUint32(header[8:12], pageSize)
	return header
}

func buildWAL(t *testing.T, fixture walFixture) []byte {
	t.Helper()
	if fixture.magic == 0 {
		fixture.magic = magicBigEndian
	}
	if fixture.pageSize == 0 {
		fixture.pageSize = 512
	}
	if fixture.pageSize%8 != 0 {
		t.Fatalf("page size %d is not checksum-aligned", fixture.pageSize)
	}
	order := fixture.checksumOrder
	if order == nil {
		var err error
		order, err = checksumOrderForFixtureMagic(fixture.magic)
		if err != nil {
			t.Fatal(err)
		}
	}
	words := fixture.checksumWords
	if words == nil {
		words = order
	}
	pageSizeField := uint32(fixture.pageSize)
	if fixture.pageSizeField != 0 {
		pageSizeField = fixture.pageSizeField
	}

	header := make([]byte, walHeaderBytes)
	binary.BigEndian.PutUint32(header[0:4], fixture.magic)
	binary.BigEndian.PutUint32(header[4:8], 3007000)
	binary.BigEndian.PutUint32(header[8:12], pageSizeField)
	binary.BigEndian.PutUint32(header[12:16], 1)
	copy(header[16:24], []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80})
	sum := fixtureChecksum(order, header[:24], checksumState{})
	words.PutUint32(header[24:28], sum.first)
	words.PutUint32(header[28:32], sum.second)

	out := append([]byte{}, header...)
	for _, frame := range fixture.frames {
		head := make([]byte, frameHeaderBytes)
		binary.BigEndian.PutUint32(head[0:4], frame.pageNumber)
		binary.BigEndian.PutUint32(head[4:8], frame.commitPages)
		copy(head[8:16], header[16:24])
		page := make([]byte, fixture.pageSize)
		for i := range page {
			page[i] = frame.fill
		}
		sum = fixtureChecksum(order, head[:8], sum)
		sum = fixtureChecksum(order, page, sum)
		words.PutUint32(head[16:20], sum.first)
		words.PutUint32(head[20:24], sum.second)
		if frame.corruptSalt {
			head[8] ^= 0xff
		}
		if frame.corruptChecksum {
			head[23] ^= 0xff
		}
		out = append(out, head...)
		out = append(out, page...)
	}
	return out
}

func checksumOrderForFixtureMagic(magic uint32) (binary.ByteOrder, error) {
	switch magic {
	case magicBigEndian:
		return binary.BigEndian, nil
	case magicLittleCksum:
		return binary.LittleEndian, nil
	default:
		return nil, errors.New("unsupported fixture magic")
	}
}

func fixtureChecksum(order binary.ByteOrder, data []byte, sum checksumState) checksumState {
	for i := 0; i+7 < len(data); i += 8 {
		sum.first += order.Uint32(data[i:i+4]) + sum.second
		sum.second += order.Uint32(data[i+4:i+8]) + sum.first
	}
	return sum
}
