// Package attachfiles locates Apple Notes attachment files on disk and
// copies them into a notes archive.
package attachfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/filecopy"
)

// DirName is the archive subdirectory that holds copied attachment files,
// relative to the archive's base directory.
const DirName = "attachments"

// Locate finds the on-disk file for a media UUID under
// <groupContainerDir>/Accounts/*/Media/<mediaID>/. The media UUID names a
// directory; the real file sits in a generation-subdirectory beneath it
// whose name is not recorded anywhere in the Notes database, so it has to be
// discovered rather than predicted. The media UUID is globally unique, so
// every Accounts/*/Media/<mediaID> match is walked regardless of which
// account it belongs to.
//
// In practice this finds exactly one file. More than one is a store shape
// this code has never observed and cannot rank without guessing which file
// is the real attachment, so it refuses and names the candidates instead of
// silently choosing one.
func Locate(groupContainerDir, mediaID string) (path string, found bool, err error) {
	mediaID = strings.TrimSpace(mediaID)
	if mediaID == "" {
		return "", false, nil
	}
	pattern := filepath.Join(groupContainerDir, "Accounts", "*", "Media", mediaID)
	dirs, err := filepath.Glob(pattern)
	if err != nil {
		return "", false, err
	}
	var files []string
	for _, dir := range dirs {
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
		if walkErr != nil {
			return "", false, walkErr
		}
	}
	if len(files) == 0 {
		return "", false, nil
	}
	if len(files) > 1 {
		return "", false, fmt.Errorf("media %s has %d files, cannot tell which is the attachment: %s", mediaID, len(files), strings.Join(files, ", "))
	}
	return files[0], true, nil
}

// Copy copies srcPath into the archive's attachments directory, keyed by
// attachment UUID: <archiveBaseDir>/attachments/<attachmentID>/<filename>.
// It skips the copy when the destination already exists with the same size,
// so a repeat sync of an unchanged corpus does not rewrite every file. It
// returns the copied file's path relative to archiveBaseDir, so the archive
// stays relocatable, and the file's size in bytes.
func Copy(archiveBaseDir, attachmentID, srcPath string) (relPath string, size int64, err error) {
	if attachmentID == "" || attachmentID == "." || attachmentID == "/" || attachmentID != filepath.Base(attachmentID) || strings.Contains(attachmentID, "..") {
		return "", 0, fmt.Errorf("attachment id %q is not a safe path component", attachmentID)
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", 0, err
	}
	rel := filepath.Join(DirName, attachmentID, filepath.Base(srcPath))
	dest := filepath.Join(archiveBaseDir, rel)
	if existing, statErr := os.Stat(dest); statErr == nil && !existing.IsDir() && existing.Size() == info.Size() {
		return rel, info.Size(), nil
	}
	if err := filecopy.CopyFile(srcPath, dest); err != nil {
		return "", 0, err
	}
	return rel, info.Size(), nil
}
