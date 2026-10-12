package render

func PhotosKnownPlacesFileCannotBeRead(knownPlacesFilePath string, readFailure error) CommandFailure {
	return archiveOrOperationFailed("The known-places file " + knownPlacesFilePath + " could not be read: " + readFailure.Error())
}

func PhotosKnownPlacesFileDoesNotMatchContract(knownPlacesFilePath string, parseFailure error) CommandFailure {
	return archiveOrOperationFailed("The known-places file " + knownPlacesFilePath + " must contain a trawl.photos.knownplaces.KnownPlacesFile in protobuf text format: " + parseFailure.Error())
}
