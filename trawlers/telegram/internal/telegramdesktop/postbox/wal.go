package postbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

const (
	sqliteWALHeaderSize      = 32
	sqliteWALFrameHeaderSize = 24
)

// readEncryptedPostboxSnapshot reads the source files without opening a SQLite
// connection that could create or write shared-memory files in Telegram's store.
func readEncryptedPostboxSnapshot(ctx context.Context, path string) ([]byte, error) {
	for range 3 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		databaseBefore, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		walPath := path + "-wal"
		walBefore, err := statOptionalPostboxWAL(walPath)
		if err != nil {
			return nil, err
		}
		database, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		wal, err := os.ReadFile(walPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		walAfter, err := statOptionalPostboxWAL(walPath)
		if err != nil {
			return nil, err
		}
		databaseAfter, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if unchangedPostboxFile(databaseBefore, databaseAfter) && unchangedPostboxFile(walBefore, walAfter) {
			return applyEncryptedPostboxWAL(database, wal)
		}
	}
	return nil, errors.New("could not read a stable snapshot of the Telegram Postbox database and write-ahead log")
}

func statOptionalPostboxWAL(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return info, err
}

func unchangedPostboxFile(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

// applyEncryptedPostboxWAL overlays complete SQLite transactions before
// SQLCipher page authentication and decryption. SQLite checksums cover the
// encrypted frame payloads. A stale or incomplete tail has no commit to apply.
func applyEncryptedPostboxWAL(database, wal []byte) ([]byte, error) {
	if len(wal) < sqliteWALHeaderSize {
		return database, nil
	}
	bigEndian := binary.BigEndian
	var checksumOrder binary.ByteOrder
	switch bigEndian.Uint32(wal[:4]) {
	case 0x377f0682:
		checksumOrder = binary.LittleEndian
	case 0x377f0683:
		checksumOrder = binary.BigEndian
	default:
		return nil, errors.New("invalid Telegram Postbox WAL header")
	}
	pageSize, err := sqlcipherPageSize(database)
	if err != nil {
		return nil, err
	}
	if bigEndian.Uint32(wal[4:8]) != 3007000 || int(bigEndian.Uint32(wal[8:12])) != pageSize {
		return nil, errors.New("write-ahead log format or page size does not match the Telegram Postbox database")
	}
	first, second := sqliteWALChecksum(checksumOrder, wal[:24], 0, 0)
	if first != bigEndian.Uint32(wal[24:28]) || second != bigEndian.Uint32(wal[28:32]) {
		return nil, errors.New("invalid Telegram Postbox WAL header checksum")
	}

	frameSize := sqliteWALFrameHeaderSize + pageSize
	var committedEnd, committedPageCount int
	for start := sqliteWALHeaderSize; start <= len(wal)-frameSize; start += frameSize {
		frame := wal[start : start+frameSize]
		if !bytes.Equal(frame[8:16], wal[16:24]) || bigEndian.Uint32(frame[:4]) == 0 {
			break
		}
		first, second = sqliteWALChecksum(checksumOrder, frame[:8], first, second)
		first, second = sqliteWALChecksum(checksumOrder, frame[sqliteWALFrameHeaderSize:], first, second)
		if first != bigEndian.Uint32(frame[16:20]) || second != bigEndian.Uint32(frame[20:24]) {
			break
		}
		if pageCount := bigEndian.Uint32(frame[4:8]); pageCount != 0 {
			committedEnd = start + frameSize
			committedPageCount = int(pageCount)
		}
	}
	if committedEnd == 0 {
		return database, nil
	}
	if committedPageCount > len(database)/pageSize+(committedEnd-sqliteWALHeaderSize)/frameSize {
		return nil, fmt.Errorf("invalid Telegram Postbox WAL commit size: %d pages", committedPageCount)
	}
	committedSize := committedPageCount * pageSize
	if committedSize > len(database) {
		database = append(database, make([]byte, committedSize-len(database))...)
	}
	for start := sqliteWALHeaderSize; start < committedEnd; start += frameSize {
		page := int(bigEndian.Uint32(wal[start : start+4]))
		if page <= committedPageCount {
			copy(database[(page-1)*pageSize:page*pageSize], wal[start+sqliteWALFrameHeaderSize:start+frameSize])
		}
	}
	return database[:committedSize], nil
}

func sqliteWALChecksum(order binary.ByteOrder, data []byte, first, second uint32) (uint32, uint32) {
	for offset := 0; offset < len(data); offset += 8 {
		first += order.Uint32(data[offset:offset+4]) + second
		second += order.Uint32(data[offset+4:offset+8]) + first
	}
	return first, second
}
