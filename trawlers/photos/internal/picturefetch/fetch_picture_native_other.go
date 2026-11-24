//go:build !darwin

package picturefetch

func fetchUprightPictureFromFile(string, bool, int32) (fetchedUprightPicture, error) {
	return fetchedUprightPicture{}, &pictureFetchFailure{pictureFetchFailureMessage: "only macOS can read pictures"}
}

func fetchUprightPictureFromApplePhotos(string, int32) (fetchedUprightPicture, error) {
	return fetchedUprightPicture{}, ErrApplePhotosAccessNotGranted
}
