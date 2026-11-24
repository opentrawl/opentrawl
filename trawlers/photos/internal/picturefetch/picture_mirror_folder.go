package picturefetch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

// PictureMirrorFolder is a local copy of the Apple Photos library that the
// person keeps, such as a nightly export to a NAS. Reading a picture from it
// saves a download from iCloud. Its layout:
//
//	<year>/<month>/<taken>-<original name>-<first 8 characters of the Apple photo UUID>.<ext>
//	<year>/<month>/<taken>-<original name>-<UUID8>_edited-<UTC edit time YYYYMMDD-HHMMSS>.<ext>
//
// The year and month are the capture month in the photo's local time.
type PictureMirrorFolder struct {
	pictureMirrorFolderPath   string
	fileNamesByCaptureMonth   map[string][]string
	pictureMirrorFolderExists bool
}

// PictureMirrorFolderIsMissingError stops a run rather than fetch every
// picture from iCloud while, for example, the NAS is not mounted.
type PictureMirrorFolderIsMissingError struct {
	PictureMirrorFolderPath string
	StatError               error
}

func (missing *PictureMirrorFolderIsMissingError) Error() string {
	return "read the picture mirror folder: " + missing.StatError.Error()
}

func NewPictureMirrorFolder(pictureMirrorFolderPath string) *PictureMirrorFolder {
	return &PictureMirrorFolder{pictureMirrorFolderPath: pictureMirrorFolderPath, fileNamesByCaptureMonth: map[string][]string{}}
}

const (
	appleMediaKindVideo           = 1
	appleAdjustmentsStateCamera   = 3
	pictureMirrorEditedMarker     = "_edited-"
	pictureMirrorEditTimeLayout   = "20060102-150405"
	largestUTCOffsetOfAnyTimeZone = 14 * time.Hour
)

var videoFileExtensions = map[string]bool{".mov": true, ".mp4": true, ".m4v": true}

// Non-picture sidecar files that can share a photo's UUID marker in a mirror.
// Extensionless files are kept because some cameras (for example DJI exports)
// omit the extension on valid JPEGs.
var mirrorSidecarFileExtensions = map[string]bool{
	".xmp":   true,
	".aae":   true,
	".json":  true,
	".plist": true,
	".txt":   true,
	".db":    true,
}

// Apple Photos shows the JPEG of a RAW and JPEG pair.
var rawPictureFileExtensions = map[string]bool{".nef": true, ".dng": true, ".cr2": true, ".cr3": true, ".arw": true, ".raf": true, ".orf": true, ".rw2": true}

// FindPictureFile returns the mirror file that shows the photo's current Apple
// edit, or "" when the mirror does not have it yet: a photo or edit newer than
// the mirror's last copy.
func (pictureMirrorFolder *PictureMirrorFolder) FindPictureFile(fetchPictureRequest *photosstep.FetchPictureRequest) (string, error) {
	if !pictureMirrorFolder.pictureMirrorFolderExists {
		if _, err := os.Stat(pictureMirrorFolder.pictureMirrorFolderPath); err != nil {
			return "", &PictureMirrorFolderIsMissingError{PictureMirrorFolderPath: pictureMirrorFolder.pictureMirrorFolderPath, StatError: err}
		}
		pictureMirrorFolder.pictureMirrorFolderExists = true
	}
	applePhotoUUID := fetchPictureRequest.GetApplePhotoUuid()
	if len(applePhotoUUID) < 8 {
		return "", nil
	}
	wantsVideoFile := fetchPictureRequest.GetAppleMediaKind() == appleMediaKindVideo
	applePhotoUUIDMarker := "-" + strings.ToUpper(applePhotoUUID[:8])
	var originalFilePath, rawOriginalFilePath, newestEditedFilePath, newestEditTime string
	for _, captureMonth := range possibleLocalCaptureMonths(fetchPictureRequest.GetCaptureTimeUtc().AsTime()) {
		fileNames, err := pictureMirrorFolder.fileNamesInCaptureMonth(captureMonth)
		if err != nil {
			return "", err
		}
		for _, fileName := range fileNames {
			fileExtension := filepath.Ext(fileName)
			lowerCaseFileExtension := strings.ToLower(fileExtension)
			if mirrorSidecarFileExtensions[lowerCaseFileExtension] || videoFileExtensions[lowerCaseFileExtension] != wantsVideoFile {
				continue
			}
			fileStem := strings.TrimSuffix(fileName, fileExtension)
			markerIndex := strings.LastIndex(fileStem, applePhotoUUIDMarker)
			if markerIndex < 0 {
				continue
			}
			filePath := filepath.Join(pictureMirrorFolder.pictureMirrorFolderPath, captureMonth, fileName)
			afterMarker := fileStem[markerIndex+len(applePhotoUUIDMarker):]
			switch {
			case strings.HasPrefix(afterMarker, pictureMirrorEditedMarker):
				editTime := strings.TrimPrefix(afterMarker, pictureMirrorEditedMarker)
				if len(editTime) == len(pictureMirrorEditTimeLayout) && editTime > newestEditTime {
					newestEditTime, newestEditedFilePath = editTime, filePath
				}
			case afterMarker == "":
				if rawPictureFileExtensions[lowerCaseFileExtension] {
					rawOriginalFilePath = filePath
				} else {
					originalFilePath = filePath
				}
			}
		}
	}
	if fetchPictureRequest.GetAppleAdjustmentsState() == 0 {
		if originalFilePath == "" {
			return rawOriginalFilePath, nil
		}
		return originalFilePath, nil
	}
	appleAdjustmentTime := fetchPictureRequest.GetAppleAdjustmentTimeUtc().AsTime().UTC().Format(pictureMirrorEditTimeLayout)
	if newestEditedFilePath != "" && newestEditTime >= appleAdjustmentTime {
		return newestEditedFilePath, nil
	}
	// Camera-adjusted videos (such as iPhone slow-motion speed ramps, state 3)
	// store adjustments in non-destructive .AAE metadata without a rendered
	// _edited- video file; the original video carries the same visual frames.
	if wantsVideoFile && fetchPictureRequest.GetAppleAdjustmentsState() == appleAdjustmentsStateCamera && originalFilePath != "" {
		return originalFilePath, nil
	}
	return "", nil
}

// possibleLocalCaptureMonths returns the "YYYY/MM" folders the capture time
// can fall in, in any time zone.
func possibleLocalCaptureMonths(captureTimeUTC time.Time) []string {
	earliestMonth := captureTimeUTC.Add(-largestUTCOffsetOfAnyTimeZone).Format("2006/01")
	latestMonth := captureTimeUTC.Add(largestUTCOffsetOfAnyTimeZone).Format("2006/01")
	if earliestMonth == latestMonth {
		return []string{earliestMonth}
	}
	return []string{earliestMonth, latestMonth}
}

func (pictureMirrorFolder *PictureMirrorFolder) fileNamesInCaptureMonth(captureMonth string) ([]string, error) {
	if fileNames, wasListed := pictureMirrorFolder.fileNamesByCaptureMonth[captureMonth]; wasListed {
		return fileNames, nil
	}
	monthEntries, err := os.ReadDir(filepath.Join(pictureMirrorFolder.pictureMirrorFolderPath, filepath.FromSlash(captureMonth)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("list the picture mirror folder: %w", err)
	}
	fileNames := make([]string, 0, len(monthEntries))
	for _, monthEntry := range monthEntries {
		if !monthEntry.IsDir() {
			fileNames = append(fileNames, monthEntry.Name())
		}
	}
	pictureMirrorFolder.fileNamesByCaptureMonth[captureMonth] = fileNames
	return fileNames, nil
}
