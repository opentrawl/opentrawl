package photos

import "errors"

var (
	ErrExportAlreadyRunning  = errors.New("photokit export already running")
	ErrPhotoKitAssetNotFound = errors.New("photokit asset not found")
)
