package applemailsource

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	fullEmlxFileSuffix    = ".emlx"
	partialEmlxFileSuffix = ".partial.emlx"
)

// AppleMailEmlxFile is one message file. A partial file holds the headers and
// text parts; Apple Mail keeps its attachments in a separate Attachments
// folder. When the full file arrives it replaces the partial one.
type AppleMailEmlxFile struct {
	EmlxFilePath                        string
	EmlxFileByteCount                   int64
	EmlxFileModificationTimeNanoseconds int64
	EmlxFileIsPartial                   bool
}

// FindAppleMailEmlxFiles walks one account folder. Message files are named
// by their Envelope Index row identifier.
func FindAppleMailEmlxFiles(accountDirectoryPath string) (map[AppleMailMessageRowIdentifier]AppleMailEmlxFile, error) {
	files := map[AppleMailMessageRowIdentifier]AppleMailEmlxFile{}
	err := filepath.WalkDir(accountDirectoryPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, fullEmlxFileSuffix) {
			return nil
		}
		isPartial := strings.HasSuffix(name, partialEmlxFileSuffix)
		rowIdentifierText := strings.TrimSuffix(name, fullEmlxFileSuffix)
		if isPartial {
			rowIdentifierText = strings.TrimSuffix(name, partialEmlxFileSuffix)
		}
		rowIdentifier, parseErr := strconv.ParseInt(rowIdentifierText, 10, 64)
		if parseErr != nil {
			return nil
		}
		if existing, found := files[AppleMailMessageRowIdentifier(rowIdentifier)]; found && !existing.EmlxFileIsPartial {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[AppleMailMessageRowIdentifier(rowIdentifier)] = AppleMailEmlxFile{
			EmlxFilePath:                        path,
			EmlxFileByteCount:                   info.Size(),
			EmlxFileModificationTimeNanoseconds: info.ModTime().UnixNano(),
			EmlxFileIsPartial:                   isPartial,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find Apple Mail message files: %w", err)
	}
	return files, nil
}

// ReadRawEmailMessageFromEmlxFile returns the RFC 5322 bytes of an .emlx
// file. The first line is the decimal byte count of the message; Apple's
// property list after the message is not part of it.
func ReadRawEmailMessageFromEmlxFile(emlxFilePath string) ([]byte, error) {
	contents, err := os.ReadFile(emlxFilePath) // #nosec G304 -- paths come from walking the Apple Mail store.
	if err != nil {
		return nil, fmt.Errorf("read Apple Mail message file: %w", err)
	}
	byteCountLine, rest, found := bytes.Cut(contents, []byte("\n"))
	byteCount, err := strconv.Atoi(strings.TrimSpace(string(byteCountLine)))
	if !found || err != nil || byteCount < 0 || byteCount > len(rest) {
		return nil, fmt.Errorf("message file %s has no valid byte count line", filepath.Base(emlxFilePath))
	}
	return rest[:byteCount], nil
}

// DetachedAttachmentByteCount returns the size of an attachment that Apple
// Mail stored beside a partial message file, at
// Attachments/<row identifier>/<MIME section>/<file name>.
func DetachedAttachmentByteCount(emlxFilePath string, rowIdentifier AppleMailMessageRowIdentifier, attachmentMIMESectionPath string) (int64, bool) {
	sectionDirectoryPath := filepath.Join(
		filepath.Dir(filepath.Dir(emlxFilePath)),
		"Attachments",
		strconv.FormatInt(int64(rowIdentifier), 10),
		attachmentMIMESectionPath,
	)
	entries, err := os.ReadDir(sectionDirectoryPath)
	if err != nil || len(entries) != 1 || !entries[0].Type().IsRegular() {
		return 0, false
	}
	info, err := entries[0].Info()
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}
