import CryptoKit
import CoreImage
import Darwin
import Foundation
import ImageIO
import Photos
import TrawlClient
import UniformTypeIdentifiers

private typealias MediaRequest = Opentrawl_Photos_Media_PhotosMediaRequest
private typealias MediaResponse = Opentrawl_Photos_Media_PhotosMediaResponse
private typealias MediaUnavailable = Opentrawl_Photos_Media_PhotosMediaUnavailable

@MainActor
final class PhotosMediaRequestProcessor {
  private let fileManager = FileManager.default
  private let maximumWireBytes = 1_048_576
  private let defaultCacheMaximumBytes: Int64 = 512 * 1_024 * 1_024
  private let defaultFreeSpaceFloorBytes: Int64 = 2 * 1_024 * 1_024 * 1_024
  private let photoKitMediaRequestTimeout: TimeInterval = 30
  private let modelImageMaximumPixelDimension = 1_200
  private let modelImageJPEGCompressionQuality = 0.95
  private let currentRenderedStillRenditionContext = CIContext()
  private let mediaReservationLedger = PhotosMediaReservationLedger()

  func process(requestDocumentURL: URL) async {
    let responseDocumentURL = Self.responseDocumentURL(for: requestDocumentURL)
    let ipcDirectory: URL
    do {
      ipcDirectory = try checkedIPCDirectory(containing: requestDocumentURL)
    } catch {
      return
    }
    do {
      try recoverAbandonedMediaFiles()
      let request = try readRequest(from: requestDocumentURL)
      let response = await perform(request, ipcDirectory: ipcDirectory)
      try writeResponse(response, to: responseDocumentURL)
    } catch let error as PhotosMediaProcessingError {
      try? writeResponse(response(for: error), to: responseDocumentURL)
    } catch {
      var response = MediaResponse()
      response.operationFailure = operationFailure(
        .invalidRequest,
        "OpenTrawl could not read the Photos media request."
      )
      try? writeResponse(response, to: responseDocumentURL)
    }
  }

  static func responseDocumentURL(for requestDocumentURL: URL) -> URL {
    requestDocumentURL.deletingPathExtension().appendingPathExtension("response.pb")
  }

  private func readRequest(from requestDocumentURL: URL) throws -> MediaRequest {
    guard requestDocumentURL.pathExtension == "opentrawl-photos-media-request" else {
      throw PhotosMediaProcessingError.invalidRequestDocument
    }
    let data = try Data(contentsOf: requestDocumentURL, options: [.mappedIfSafe])
    guard !data.isEmpty, data.count <= maximumWireBytes else {
      throw PhotosMediaProcessingError.invalidRequestDocument
    }
    return try MediaRequest(serializedBytes: data)
  }

  private func writeResponse(_ response: MediaResponse, to responseDocumentURL: URL) throws {
    let data = try response.serializedData()
    guard data.count <= maximumWireBytes else {
      throw PhotosMediaProcessingError.responseTooLarge
    }
    let temporaryURL = responseDocumentURL.appendingPathExtension("writing")
    try? fileManager.removeItem(at: temporaryURL)
    try data.write(to: temporaryURL, options: [.atomic])
    try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: temporaryURL.path)
    try? fileManager.removeItem(at: responseDocumentURL)
    try fileManager.moveItem(at: temporaryURL, to: responseDocumentURL)
  }

  private func perform(_ request: MediaRequest, ipcDirectory: URL) async -> MediaResponse {
    switch request.operation {
    case .readPhotoLibraryAccess:
      return accessResponse(for: PHPhotoLibrary.authorizationStatus(for: .readWrite))
    case .requestPhotoLibraryAccess:
      let status = await PHPhotoLibrary.requestAuthorization(for: .readWrite)
      return accessResponse(for: status)
    case .inspectPhotoAssetReadiness(let operation):
      return inspectReadiness(operation)
    case .acquireCurrentRenderedStill(let operation):
      return await acquireCurrentRenderedStill(operation, ipcDirectory: ipcDirectory)
    case .inspectImmutableOriginalImageFacts(let operation):
      return await inspectImmutableOriginalImageFacts(operation)
    case .releaseCurrentRenderedStillLease(let operation):
      return releaseCurrentRenderedStillLease(operation, ipcDirectory: ipcDirectory)
    case nil:
      var response = MediaResponse()
      response.operationFailure = operationFailure(.invalidRequest, "The Photos media request has no operation.")
      return response
    }
  }

  private func accessResponse(for authorizationStatus: PHAuthorizationStatus) -> MediaResponse {
    var access = Opentrawl_Photos_Media_PhotoLibraryAccessResult()
    access.state = photoLibraryAccessState(authorizationStatus)
    var response = MediaResponse()
    response.photoLibraryAccess = access
    return response
  }

  private func inspectReadiness(
    _ request: Opentrawl_Photos_Media_InspectPhotoAssetReadinessRequest
  ) -> MediaResponse {
    if let unavailable = photosAccessUnavailableResponse() { return unavailable }
    guard !request.photoAssetLocalIdentifier.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
      return operationFailureResponse(.invalidRequest, "The photo readiness request has no asset identifier.")
    }
    guard let asset = photoAsset(localIdentifier: request.photoAssetLocalIdentifier) else {
      return unavailableResponse(.assetNotFound, "OpenTrawl could not find this photo in Apple Photos.")
    }
    guard asset.mediaType == .image else {
      return unavailableResponse(.notAnImage, "This Apple Photos asset is not an image.")
    }
    var readiness = Opentrawl_Photos_Media_PhotoAssetReadiness()
    readiness.photoAssetLocalIdentifier = request.photoAssetLocalIdentifier
    readiness.pixelWidth = UInt64(asset.pixelWidth)
    readiness.pixelHeight = UInt64(asset.pixelHeight)
    if let creationDate = asset.creationDate {
      readiness.creationTime = .init(date: creationDate)
    }
    if let modificationDate = asset.modificationDate {
      readiness.modificationTime = .init(date: modificationDate)
    }
    var response = MediaResponse()
    response.photoAssetReadiness = readiness
    return response
  }

  private func acquireCurrentRenderedStill(
    _ request: Opentrawl_Photos_Media_AcquireCurrentRenderedStillRequest,
    ipcDirectory: URL
  ) async -> MediaResponse {
    if let unavailable = photosAccessUnavailableResponse() { return unavailable }
    guard !request.photoAssetLocalIdentifier.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
      return operationFailureResponse(.invalidRequest, "The current rendered image request is incomplete.")
    }
    guard let asset = photoAsset(localIdentifier: request.photoAssetLocalIdentifier) else {
      return unavailableResponse(.assetNotFound, "OpenTrawl could not find this photo in Apple Photos.")
    }
    guard asset.mediaType == .image else {
      return unavailableResponse(.notAnImage, "This Apple Photos asset is not an image.")
    }
    let indexedModificationTimeStillMatches: Bool
    if request.hasExpectedPhotoModificationTime {
      indexedModificationTimeStillMatches = asset.modificationDate.map {
        abs($0.timeIntervalSince(request.expectedPhotoModificationTime.date)) < 0.001
      } ?? false
    } else {
      indexedModificationTimeStillMatches = asset.modificationDate == nil
    }
    guard indexedModificationTimeStillMatches else {
      return operationFailureResponse(
        .indexedSourceChanged,
        "The photo changed after OpenTrawl indexed it.",
        indexedPhotoModificationTime: request.hasExpectedPhotoModificationTime
          ? request.expectedPhotoModificationTime.date
          : nil,
        currentPhotoModificationTime: asset.modificationDate
      )
    }
    do {
      let derivation = try await modelSupportedCurrentRenderedStill(
        for: asset,
        allowNetwork: request.allowIcloudNetworkAccess
      )
      let rendered = derivation.output
      let leaseIdentifier = UUID().uuidString.lowercased()
      let leaseURL = ipcDirectory.appendingPathComponent(leaseIdentifier).appendingPathExtension("image")
      try reserveMediaBytes(
        identifier: leaseIdentifier,
        byteCount: rendered.byteCount,
        at: ipcDirectory
      )
      do {
        try rendered.data.write(to: leaseURL, options: [.atomic])
        try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: leaseURL.path)
        try mediaReservationLedger.recordMaterializedBytes(
          identifier: leaseIdentifier,
          byteCount: rendered.byteCount
        )
      } catch {
        releaseMediaBytes(identifier: leaseIdentifier)
        try? fileManager.removeItem(at: leaseURL)
        throw PhotosMediaProcessingError.cacheIO
      }
      var lease = Opentrawl_Photos_Media_CurrentRenderedStillLease()
      lease.leaseIdentifier = leaseIdentifier
      lease.checkedFilePath = leaseURL.path
      lease.byteCount = UInt64(rendered.byteCount)
      lease.sha256 = rendered.sha256
      lease.uniformTypeIdentifier = rendered.uniformTypeIdentifier
      lease.imageOrientation = imageOrientation(rendered.orientation)
      lease.pixelWidth = UInt64(rendered.pixelWidth)
      lease.pixelHeight = UInt64(rendered.pixelHeight)
      var receipt = Opentrawl_Photos_Media_CurrentRenderedStillDerivationReceipt()
      receipt.request = request
      receipt.photoKitVersion = .current
      receipt.photoKitDeliveryMode = .highQuality
      receipt.photoKitResizeMode = .none
      receipt.photoKitRequestIsSynchronous = false
      receipt.sourcePixelWidth = UInt64(derivation.sourcePixelWidth)
      receipt.sourcePixelHeight = UInt64(derivation.sourcePixelHeight)
      receipt.sourceImageOrientation = imageOrientation(derivation.sourceOrientation)
      receipt.jpegMaximumPixelDimension = UInt64(derivation.jpegMaximumPixelDimension)
      receipt.jpegCompressionQuality = modelImageJPEGCompressionQuality
      receipt.outputUniformTypeIdentifier = rendered.uniformTypeIdentifier
      receipt.finalJpegSha256 = rendered.sha256
      lease.derivationReceipt = receipt
      var response = MediaResponse()
      response.currentRenderedStillLease = lease
      return response
    } catch let error as PhotosMediaProcessingError {
      return response(for: error)
    } catch {
      return operationFailureResponse(.photosProviderFailure, "Apple Photos could not provide the current rendered image.")
    }
  }

  private func inspectImmutableOriginalImageFacts(
    _ request: Opentrawl_Photos_Media_InspectImmutableOriginalImageFactsRequest
  ) async -> MediaResponse {
    if let unavailable = photosAccessUnavailableResponse() {
      return immutableOriginalOutcomeResponse(request: request, operationResponse: unavailable)
    }
    guard !request.photoAssetLocalIdentifier.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
      return immutableOriginalOutcomeResponse(
        request: request,
        operationResponse: operationFailureResponse(.invalidRequest, "The immutable original request is incomplete.")
      )
    }
    guard let asset = photoAsset(localIdentifier: request.photoAssetLocalIdentifier) else {
      return immutableOriginalOutcomeResponse(
        request: request,
        operationResponse: unavailableResponse(.assetNotFound, "OpenTrawl could not find this photo in Apple Photos.")
      )
    }
    guard asset.mediaType == .image else {
      return immutableOriginalOutcomeResponse(
        request: request,
        operationResponse: unavailableResponse(.notAnImage, "This Apple Photos asset is not an image.")
      )
    }

    let photoKitResources = PHAssetResource.assetResources(for: asset)
    let candidateReceipts = photoKitResources.enumerated().map { position, resource in
      var candidate = Opentrawl_Photos_Media_PhotoKitOriginalResourceCandidate()
      candidate.providerPosition = Int32(position)
      candidate.photoKitResourceType = Int32(resource.type.rawValue)
      candidate.filename = resource.originalFilename
      candidate.uniformTypeIdentifier = resource.uniformTypeIdentifier
      return candidate
    }

    let matchingPhotoKitCandidatePositions = candidateReceipts.indices.filter { position in
      photoKitResources[position].type == .photo
    }
    guard matchingPhotoKitCandidatePositions.count == 1 else {
      let operationResponse: MediaResponse
      if photoKitResources.contains(where: { $0.type == .photo }) {
        operationResponse = operationFailureResponse(
          .indexedSourceChanged,
          "Apple Photos exposed more than one image original."
        )
      } else {
        operationResponse = unavailableResponse(
          .immutableOriginalNotFound,
          "Apple Photos did not expose an immutable image original."
        )
      }
      return immutableOriginalOutcomeResponse(
        request: request,
        candidates: candidateReceipts,
        operationResponse: operationResponse
      )
    }

    let selectedPhotoKitCandidatePosition = matchingPhotoKitCandidatePositions[0]
    let resource = photoKitResources[selectedPhotoKitCandidatePosition]
    do {
      let cache: CheckedPhotosMediaCache
      do {
        cache = try checkedCache()
      } catch let error as PhotosMediaProcessingError {
        throw error
      } catch {
        throw PhotosMediaProcessingError.cacheIO
      }
      let reservationIdentifier = UUID().uuidString.lowercased()
      try beginUnknownSizeMediaReservation(identifier: reservationIdentifier, at: cache.root)
      let temporaryURL = cache.root.appendingPathComponent(".\(reservationIdentifier).original-reading")
      defer {
        try? fileManager.removeItem(at: temporaryURL)
        releaseMediaBytes(identifier: reservationIdentifier)
      }
      let activeMediaReservationLedger = mediaReservationLedger
      let maximumMediaBytes = cache.maximumBytes
      let freeSpaceFloorBytes = defaultFreeSpaceFloorBytes
      let reservationDirectory = cache.root
      let reserveAdditionalBytes: @Sendable (Int64) throws -> Void = { additionalByteCount in
        try activeMediaReservationLedger.increaseReservation(
          identifier: reservationIdentifier,
          additionalByteCount: additionalByteCount,
          maximumAggregateByteCount: maximumMediaBytes,
          reservationDirectory: reservationDirectory,
          freeSpaceFloorByteCount: freeSpaceFloorBytes
        )
      }
      let recordMaterializedBytes: @Sendable (Int64) throws -> Void = { byteCount in
        try activeMediaReservationLedger.recordMaterializedBytes(
          identifier: reservationIdentifier,
          byteCount: byteCount
        )
      }
      try await writeOriginalResource(
        resource,
        to: temporaryURL,
        allowNetwork: request.allowIcloudNetworkAccess,
        expectedByteCount: 0,
        maximumByteCount: cache.maximumBytes,
        reserveAdditionalBytes: reserveAdditionalBytes,
        recordMaterializedBytes: recordMaterializedBytes
      )
      let facts = try imageFacts(at: temporaryURL, uniformTypeIdentifier: resource.uniformTypeIdentifier)
      return immutableOriginalOutcomeResponse(
        request: request,
        candidates: candidateReceipts,
        selectedPhotoKitCandidatePosition: selectedPhotoKitCandidatePosition,
        facts: facts
      )
    } catch let error as PhotosMediaProcessingError {
      return immutableOriginalOutcomeResponse(
        request: request,
        candidates: candidateReceipts,
        selectedPhotoKitCandidatePosition: selectedPhotoKitCandidatePosition,
        operationResponse: response(for: error)
      )
    } catch {
      return immutableOriginalOutcomeResponse(
        request: request,
        candidates: candidateReceipts,
        selectedPhotoKitCandidatePosition: selectedPhotoKitCandidatePosition,
        operationResponse: operationFailureResponse(
          .photosProviderFailure,
          "Apple Photos could not inspect the immutable image original."
        )
      )
    }
  }

  private func releaseCurrentRenderedStillLease(
    _ request: Opentrawl_Photos_Media_ReleaseCurrentRenderedStillLeaseRequest,
    ipcDirectory: URL
  ) -> MediaResponse {
    guard UUID(uuidString: request.leaseIdentifier) != nil else {
      return operationFailureResponse(.invalidRequest, "The current rendered image lease is invalid.")
    }
    let leaseURL = ipcDirectory.appendingPathComponent(request.leaseIdentifier).appendingPathExtension("image")
    do {
      try fileManager.removeItem(at: leaseURL)
    } catch {
      return operationFailureResponse(.cacheIo, "OpenTrawl could not release the current rendered image.")
    }
    releaseMediaBytes(identifier: request.leaseIdentifier.lowercased())
    var released = Opentrawl_Photos_Media_ReleasedCurrentRenderedStillLease()
    released.leaseIdentifier = request.leaseIdentifier
    var response = MediaResponse()
    response.releasedCurrentRenderedStillLease = released
    return response
  }

  private func photoAsset(localIdentifier: String) -> PHAsset? {
    guard !localIdentifier.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
    return PHAsset.fetchAssets(withLocalIdentifiers: [localIdentifier], options: nil).firstObject
  }

  private func modelSupportedCurrentRenderedStill(
    for asset: PHAsset,
    allowNetwork: Bool
  ) async throws -> CurrentRenderedStillDerivation {
    let options = PHImageRequestOptions()
    options.version = .current
    options.deliveryMode = .highQualityFormat
    options.resizeMode = .none
    options.isSynchronous = false
    options.isNetworkAccessAllowed = allowNetwork
    let outcome = await requestCurrentRenderedStill(
      for: asset,
      options: options,
      timeout: photoKitMediaRequestTimeout
    )
    let result: RenderedPhotoKitResult
    switch outcome {
    case .completed(let completedResult):
      result = completedResult
    case .networkAccessRequired:
      throw allowNetwork ? PhotosMediaProcessingError.photosProviderFailure : PhotosMediaProcessingError.mediaNotLocal
    case .cancelled:
      throw PhotosMediaProcessingError.photosCancelled
    case .providerFailure:
      throw PhotosMediaProcessingError.photosProviderFailure
    case .timedOut:
      throw PhotosMediaProcessingError.photosTimeout
    }
    if result.isInCloud, result.data == nil {
      throw allowNetwork ? PhotosMediaProcessingError.photosProviderFailure : PhotosMediaProcessingError.mediaNotLocal
    }
    guard let data = result.data, !data.isEmpty else {
      throw PhotosMediaProcessingError.photosProviderFailure
    }
    let checked = try checkedCurrentRenderedStillData(data, photoKitOrientation: result.orientation)
    let output = try modelJPEGCurrentRenderedStill(
      from: data,
      sourcePixelWidth: checked.pixelWidth,
      sourcePixelHeight: checked.pixelHeight,
      sourceOrientation: checked.orientation
    )
    return CurrentRenderedStillDerivation(
      output: output,
      sourcePixelWidth: checked.pixelWidth,
      sourcePixelHeight: checked.pixelHeight,
      sourceOrientation: checked.orientation,
      jpegMaximumPixelDimension: min(
        Int64(modelImageMaximumPixelDimension),
        max(checked.pixelWidth, checked.pixelHeight)
      )
    )
  }

  private func modelJPEGCurrentRenderedStill(
    from sourceData: Data,
    sourcePixelWidth: Int64,
    sourcePixelHeight: Int64,
    sourceOrientation: Int32
  ) throws -> CheckedImageFile {
    let sourceMaximumPixelDimension = max(sourcePixelWidth, sourcePixelHeight)
    let outputMaximumPixelDimension = min(
      Int64(modelImageMaximumPixelDimension),
      sourceMaximumPixelDimension
    )
    guard outputMaximumPixelDimension > 0,
          let source = CGImageSourceCreateWithData(sourceData as CFData, nil),
          let thumbnail = CGImageSourceCreateThumbnailAtIndex(source, 0, [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: false,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: outputMaximumPixelDimension,
          ] as CFDictionary)
    else { throw PhotosMediaProcessingError.unsupportedImage }

    let uprightThumbnail = CIImage(cgImage: thumbnail).oriented(forExifOrientation: sourceOrientation)
    guard let uprightImage = currentRenderedStillRenditionContext.createCGImage(
      uprightThumbnail,
      from: uprightThumbnail.extent
    ) else { throw PhotosMediaProcessingError.unsupportedImage }

    let output = NSMutableData()
    guard let destination = CGImageDestinationCreateWithData(
      output,
      UTType.jpeg.identifier as CFString,
      1,
      nil
    ) else { throw PhotosMediaProcessingError.unsupportedImage }
    CGImageDestinationAddImage(destination, uprightImage, [
      kCGImageDestinationLossyCompressionQuality: modelImageJPEGCompressionQuality,
      kCGImagePropertyOrientation: 1,
    ] as CFDictionary)
    guard CGImageDestinationFinalize(destination) else {
      throw PhotosMediaProcessingError.unsupportedImage
    }
    var checked = try checkedImageData(output as Data)
    if checked.orientation == 0 {
      checked.orientation = 1
    }
    guard checked.uniformTypeIdentifier == UTType.jpeg.identifier,
          checked.pixelWidth > 0,
          checked.pixelHeight > 0,
          max(checked.pixelWidth, checked.pixelHeight) <= outputMaximumPixelDimension,
          checked.orientation == 1
    else { throw PhotosMediaProcessingError.unsupportedImage }
    return checked
  }

  private func requestCurrentRenderedStill(
    for asset: PHAsset,
    options: PHImageRequestOptions,
    timeout: TimeInterval
  ) async -> RenderedPhotoKitOutcome {
    let manager = PHImageManager.default()
    let completion = OneShotContinuation<RenderedPhotoKitOutcome>()
    let cancellation = PhotosImageRequestCancellation(manager: manager)
    return await withTaskCancellationHandler {
      await withCheckedContinuation { continuation in
        completion.install(continuation)
        let requestIdentifier = manager.requestImageDataAndOrientation(
          for: asset,
          options: options,
          resultHandler: PhotoKitCallbackFactory.renderedStillResultHandler(completion: completion)
        )
        cancellation.setRequestIdentifier(requestIdentifier)
        PhotoKitCallbackFactory.scheduleRenderedStillTimeout(
          after: timeout,
          completion: completion,
          cancellation: cancellation
        )
      }
    } onCancel: {
      if completion.resume(returning: .cancelled) {
        cancellation.cancel()
      }
    }
  }

  private func writeOriginalResource(
    _ resource: PHAssetResource,
    to destinationURL: URL,
    allowNetwork: Bool,
    expectedByteCount: Int64,
    maximumByteCount: Int64,
    reserveAdditionalBytes: (@Sendable (Int64) throws -> Void)?,
    recordMaterializedBytes: @escaping @Sendable (Int64) throws -> Void
  ) async throws {
    guard fileManager.createFile(atPath: destinationURL.path, contents: nil) else {
      throw PhotosMediaProcessingError.cacheIO
    }
    let writer: BoundedOriginalResourceWriter
    do {
      writer = try BoundedOriginalResourceWriter(
        destinationURL: destinationURL,
        expectedByteCount: expectedByteCount,
        maximumByteCount: maximumByteCount,
        reserveAdditionalBytes: reserveAdditionalBytes,
        recordMaterializedBytes: recordMaterializedBytes
      )
    } catch {
      throw PhotosMediaProcessingError.cacheIO
    }
    let completionError = await streamOriginalResource(
      resource,
      allowNetwork: allowNetwork,
      writer: writer,
      timeout: photoKitMediaRequestTimeout
    )
    do {
      try writer.close()
    } catch {
      throw PhotosMediaProcessingError.cacheIO
    }
    if let writerError = writer.failureSnapshot() { throw writerError }
    switch completionError {
    case .completed:
      break
    case .networkAccessRequired:
      throw allowNetwork ? PhotosMediaProcessingError.photosProviderFailure : PhotosMediaProcessingError.mediaNotLocal
    case .resourceMissing:
      throw PhotosMediaProcessingError.immutableOriginalNotFound
    case .cancelled:
      throw PhotosMediaProcessingError.photosCancelled
    case .providerFailure:
      throw PhotosMediaProcessingError.photosProviderFailure
    case .timedOut:
      throw PhotosMediaProcessingError.photosTimeout
    }
    guard expectedByteCount == 0 || writer.byteCountSnapshot() == expectedByteCount else {
      throw PhotosMediaProcessingError.indexedOriginalChanged
    }
    let byteCount: Int
    do {
      byteCount = try destinationURL.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
    } catch {
      throw PhotosMediaProcessingError.cacheIO
    }
    guard byteCount > 0 else { throw PhotosMediaProcessingError.mediaNotLocal }
  }

  private func streamOriginalResource(
    _ resource: PHAssetResource,
    allowNetwork: Bool,
    writer: BoundedOriginalResourceWriter,
    timeout: TimeInterval
  ) async -> OriginalResourcePhotoKitOutcome {
    let options = PHAssetResourceRequestOptions()
    options.isNetworkAccessAllowed = allowNetwork
    let manager = PHAssetResourceManager.default()
    let completion = OneShotContinuation<OriginalResourcePhotoKitOutcome>()
    let cancellation = PhotosResourceRequestCancellation(manager: manager)
    return await withTaskCancellationHandler {
      await withCheckedContinuation { continuation in
        completion.install(continuation)
        let requestIdentifier = manager.requestData(
          for: resource,
          options: options,
          dataReceivedHandler: PhotoKitCallbackFactory.originalResourceDataHandler(
            writer: writer,
            completion: completion,
            cancellation: cancellation
          ),
          completionHandler: PhotoKitCallbackFactory.originalResourceCompletionHandler(
            completion: completion
          )
        )
        cancellation.setRequestIdentifier(requestIdentifier)
        PhotoKitCallbackFactory.scheduleOriginalResourceTimeout(
          after: timeout,
          completion: completion,
          cancellation: cancellation
        )
      }
    } onCancel: {
      if completion.resume(returning: .cancelled) {
        cancellation.cancel()
      }
    }
  }

  private func imageFacts(
    at url: URL,
    uniformTypeIdentifier: String
  ) throws -> Opentrawl_Photos_Media_ImmutableOriginalImageFacts {
    let data = try Data(contentsOf: url, options: [.mappedIfSafe])
    guard let source = CGImageSourceCreateWithURL(url as CFURL, nil),
          let rawProperties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [String: Any]
    else {
      throw PhotosMediaProcessingError.unsupportedImage
    }
    var facts = Opentrawl_Photos_Media_ImmutableOriginalImageFacts()
    facts.byteCount = UInt64(data.count)
    facts.sha256 = Data(SHA256.hash(data: data))
    facts.uniformTypeIdentifier = uniformTypeIdentifier
    facts.pixelWidth = UInt64(max(integerValue(rawProperties[kCGImagePropertyPixelWidth as String]), 0))
    facts.pixelHeight = UInt64(max(integerValue(rawProperties[kCGImagePropertyPixelHeight as String]), 0))
    facts.imageOrientation = imageOrientation(Int32(integerValue(rawProperties[kCGImagePropertyOrientation as String])))
    let tiff = dictionary(rawProperties[kCGImagePropertyTIFFDictionary as String])
    let exif = dictionary(rawProperties[kCGImagePropertyExifDictionary as String])
    if let cameraManufacturerName = meaningfulText(tiff[kCGImagePropertyTIFFMake as String]) {
      facts.cameraManufacturerName = cameraManufacturerName
    }
    if let cameraModelName = meaningfulText(tiff[kCGImagePropertyTIFFModel as String]) {
      facts.cameraModelName = cameraModelName
    }
    if let lensModelName = meaningfulText(exif[kCGImagePropertyExifLensModel as String]) {
      facts.lensModelName = lensModelName
    }
    if let focalLengthMillimetres = decimalValue(exif[kCGImagePropertyExifFocalLength as String]) {
      facts.focalLengthMillimetres = focalLengthMillimetres
    }
    if let focalLength35MillimetreEquivalent = decimalValue(exif[kCGImagePropertyExifFocalLenIn35mmFilm as String]) {
      facts.focalLength35MillimetreEquivalent = focalLength35MillimetreEquivalent
    }
    if let apertureFNumber = decimalValue(exif[kCGImagePropertyExifFNumber as String]) {
      facts.apertureFNumber = apertureFNumber
    }
    if let exposureTimeSeconds = decimalValue(exif[kCGImagePropertyExifExposureTime as String]) {
      facts.exposureTimeSeconds = exposureTimeSeconds
    }
    if let iso = (exif[kCGImagePropertyExifISOSpeedRatings as String] as? [NSNumber])?.first {
      facts.isoSpeedRating = iso.int64Value
    }
    facts.properties = typedImageMetadataProperties(rawProperties)
    return facts
  }

  private func typedImageMetadataProperties(_ root: [String: Any]) -> [Opentrawl_Photos_Media_ImageMetadataProperty] {
    let namespaces: [(String, CFString)] = [
      ("TIFF", kCGImagePropertyTIFFDictionary),
      ("Exif", kCGImagePropertyExifDictionary),
      ("ExifAux", kCGImagePropertyExifAuxDictionary),
      ("GPS", kCGImagePropertyGPSDictionary),
      ("IPTC", kCGImagePropertyIPTCDictionary),
      ("JFIF", kCGImagePropertyJFIFDictionary),
      ("PNG", kCGImagePropertyPNGDictionary),
    ]
    return namespaces.flatMap { namespace, key in
      dictionary(root[key as String]).keys.sorted().compactMap { propertyName in
        guard propertyName.lowercased() != "makernote",
              let value = typedMetadataValue(dictionary(root[key as String])[propertyName])
        else { return nil }
        var property = Opentrawl_Photos_Media_ImageMetadataProperty()
        property.imageIoNamespace = namespace
        property.propertyName = propertyName
        property.value = value
        return property
      }
    }
  }

  private func typedMetadataValue(_ raw: Any?) -> Opentrawl_Photos_Media_ImageMetadataValue? {
    guard let raw else { return nil }
    var typed = Opentrawl_Photos_Media_ImageMetadataValue()
    if let text = meaningfulText(raw), text.utf8.count <= 4_096 {
      typed.text = text
      return typed
    }
    if let number = raw as? NSNumber {
      if CFGetTypeID(number) == CFBooleanGetTypeID() {
        typed.boolean = number.boolValue
      } else if number.doubleValue.rounded() == number.doubleValue {
        typed.integer = number.int64Value
      } else {
        typed.decimal = number.doubleValue
      }
      return typed
    }
    if let date = raw as? Date {
      typed.time = .init(date: date)
      return typed
    }
    if let strings = raw as? [String], strings.count <= 256 {
      var list = Opentrawl_Photos_Media_ImageMetadataTextList()
      list.values = strings.filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && $0.utf8.count <= 4_096 }
      guard !list.values.isEmpty else { return nil }
      typed.textList = list
      return typed
    }
    if let numbers = raw as? [NSNumber], numbers.count <= 256 {
      if numbers.allSatisfy({ $0.doubleValue.rounded() == $0.doubleValue }) {
        var list = Opentrawl_Photos_Media_ImageMetadataIntegerList()
        list.values = numbers.map(\.int64Value)
        typed.integerList = list
      } else {
        var list = Opentrawl_Photos_Media_ImageMetadataDecimalList()
        list.values = numbers.map(\.doubleValue)
        typed.decimalList = list
      }
      return typed
    }
    return nil
  }

  private func checkedImageData(_ data: Data) throws -> CheckedImageFile {
    guard !data.isEmpty,
          let source = CGImageSourceCreateWithData(data as CFData, nil),
          let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [String: Any]
    else { throw PhotosMediaProcessingError.unsupportedImage }
    return CheckedImageFile(
      data: data,
      byteCount: Int64(data.count),
      sha256: Data(SHA256.hash(data: data)),
      uniformTypeIdentifier: (CGImageSourceGetType(source) as String?) ?? "public.image",
      orientation: Int32(integerValue(properties[kCGImagePropertyOrientation as String])),
      pixelWidth: integerValue(properties[kCGImagePropertyPixelWidth as String]),
      pixelHeight: integerValue(properties[kCGImagePropertyPixelHeight as String])
    )
  }

  private func checkedCurrentRenderedStillData(
    _ data: Data,
    photoKitOrientation: Int32
  ) throws -> CheckedImageFile {
    var checked = try checkedImageData(data)
    if checked.orientation < 1 || checked.orientation > 8 {
      checked.orientation = photoKitOrientation
    }
    guard checked.pixelWidth > 0,
          checked.pixelHeight > 0,
          checked.orientation >= 1,
          checked.orientation <= 8
    else { throw PhotosMediaProcessingError.unsupportedImage }
    return checked
  }

  private func checkedCache() throws -> CheckedPhotosMediaCache {
    let maximumBytes = defaultCacheMaximumBytes
    let root: URL
    #if DEBUG
    if let developmentRoot = UserDefaults.standard.string(forKey: "PhotosMediaDevelopmentCacheRoot"),
       !developmentRoot.isEmpty
    {
      let candidate = URL(fileURLWithPath: developmentRoot, isDirectory: true).standardizedFileURL
      guard candidate.path.hasPrefix("/Volumes/"), candidate.pathComponents.count >= 4 else {
        throw PhotosMediaProcessingError.invalidCacheRoot
      }
      root = candidate.appendingPathComponent("OpenTrawl/PhotosMedia", isDirectory: true)
    } else {
      root = try fileManager.url(for: .cachesDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        .appendingPathComponent("OpenTrawl/PhotosMedia", isDirectory: true)
    }
    #else
    root = try fileManager.url(for: .cachesDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
      .appendingPathComponent("OpenTrawl/PhotosMedia", isDirectory: true)
    #endif
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    try fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: root.path)
    return CheckedPhotosMediaCache(root: root, maximumBytes: maximumBytes)
  }

  private func recoverAbandonedMediaFiles() throws {
    do {
      try recoverAbandonedMediaFilesInCheckedDirectories()
    } catch let error as PhotosMediaProcessingError {
      throw error
    } catch {
      throw PhotosMediaProcessingError.cacheIO
    }
  }

  private func recoverAbandonedMediaFilesInCheckedDirectories() throws {
    let cache = try checkedCache()
    for candidate in try fileManager.contentsOfDirectory(
      at: cache.root,
      includingPropertiesForKeys: nil,
      options: [.skipsSubdirectoryDescendants]
    ) where candidate.lastPathComponent.hasPrefix(".")
      && candidate.lastPathComponent.hasSuffix(".original-reading")
    {
      let identifier = candidate.lastPathComponent
        .dropFirst()
        .dropLast(".original-reading".count)
        .lowercased()
      if !mediaReservationLedger.containsReservation(identifier: identifier) {
        try removeAbandonedMediaFileIfPresent(candidate)
      }
    }

    let temporaryDirectory = fileManager.temporaryDirectory
    for sessionDirectory in try fileManager.contentsOfDirectory(
      at: temporaryDirectory,
      includingPropertiesForKeys: nil,
      options: [.skipsSubdirectoryDescendants]
    ) where sessionDirectory.lastPathComponent.hasPrefix("opentrawl-photos-media-") {
      guard let attributes = try? fileManager.attributesOfItem(atPath: sessionDirectory.path) else { continue }
      guard attributes[.type] as? FileAttributeType == .typeDirectory,
            (attributes[.ownerAccountID] as? NSNumber)?.uint32Value == getuid(),
            ((attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0) & 0o777 == 0o700
      else { continue }
      if try sessionHasLivePhotosMediaClient(sessionDirectory) {
        try reconstructLiveMediaReservations(
          in: sessionDirectory,
          maximumBytes: cache.maximumBytes
        )
        continue
      }
      let abandonedLeaseIdentifiers = (try? fileManager.contentsOfDirectory(
        at: sessionDirectory,
        includingPropertiesForKeys: nil,
        options: [.skipsSubdirectoryDescendants]
      ))?.compactMap { candidate -> String? in
        guard candidate.pathExtension == "image" else { return nil }
        let identifier = candidate.deletingPathExtension().lastPathComponent.lowercased()
        return UUID(uuidString: identifier) == nil ? nil : identifier
      } ?? []
      try fileManager.removeItem(at: sessionDirectory)
      for identifier in abandonedLeaseIdentifiers {
        releaseMediaBytes(identifier: identifier)
      }
    }
  }

  private func reconstructLiveMediaReservations(
    in sessionDirectory: URL,
    maximumBytes: Int64
  ) throws {
    for candidate in try fileManager.contentsOfDirectory(
      at: sessionDirectory,
      includingPropertiesForKeys: nil,
      options: [.skipsSubdirectoryDescendants]
    ) where candidate.pathExtension == "image" {
      let identifier = candidate.deletingPathExtension().lastPathComponent.lowercased()
      guard UUID(uuidString: identifier) != nil else { continue }
      let attributes = try fileManager.attributesOfItem(atPath: candidate.path)
      guard attributes[.type] as? FileAttributeType == .typeRegular,
            (attributes[.ownerAccountID] as? NSNumber)?.uint32Value == getuid(),
            ((attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0) & 0o777 == 0o600,
            let byteCount = (attributes[.size] as? NSNumber)?.int64Value,
            byteCount > 0,
            byteCount <= maximumBytes
      else { throw PhotosMediaProcessingError.cacheIO }
      try mediaReservationLedger.reconstructReservation(
        identifier: identifier,
        byteCount: byteCount,
        maximumAggregateByteCount: maximumBytes
      )
    }
  }

  private func sessionHasLivePhotosMediaClient(_ sessionDirectory: URL) throws -> Bool {
    let clientLockURL = sessionDirectory.appendingPathComponent("client.lock")
    let fileDescriptor = Darwin.open(clientLockURL.path, O_RDWR)
    if fileDescriptor == -1 {
      if errno == ENOENT { return false }
      throw PhotosMediaProcessingError.cacheIO
    }
    defer { Darwin.close(fileDescriptor) }
    var clientWriteLock = flock(
      l_start: 0,
      l_len: 0,
      l_pid: 0,
      l_type: Int16(F_WRLCK),
      l_whence: Int16(SEEK_SET)
    )
    if Darwin.fcntl(fileDescriptor, F_SETLK, &clientWriteLock) == 0 {
      clientWriteLock.l_type = Int16(F_UNLCK)
      _ = Darwin.fcntl(fileDescriptor, F_SETLK, &clientWriteLock)
      return false
    }
    if errno == EACCES || errno == EAGAIN { return true }
    throw PhotosMediaProcessingError.cacheIO
  }

  private func removeAbandonedMediaFileIfPresent(_ url: URL) throws {
    do {
      try fileManager.removeItem(at: url)
    } catch {
      if fileManager.fileExists(atPath: url.path) { throw error }
    }
  }

  private func checkedIPCDirectory(containing requestDocumentURL: URL) throws -> URL {
    let directory = requestDocumentURL.deletingLastPathComponent().resolvingSymlinksInPath().standardizedFileURL
    guard directory.lastPathComponent.hasPrefix("opentrawl-photos-media-"),
          requestDocumentURL.lastPathComponent == "request.opentrawl-photos-media-request"
    else { throw PhotosMediaProcessingError.invalidRequestDocument }
    let attributes = try fileManager.attributesOfItem(atPath: directory.path)
    guard (attributes[.ownerAccountID] as? NSNumber)?.uint32Value == getuid(),
          ((attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0) & 0o777 == 0o700
    else { throw PhotosMediaProcessingError.invalidRequestDocument }
    guard try sessionHasLivePhotosMediaClient(directory) else {
      throw PhotosMediaProcessingError.invalidRequestDocument
    }
    return directory
  }

  private func reserveMediaBytes(
    identifier: String,
    byteCount: Int64,
    at directory: URL
  ) throws {
    let normalizedIdentifier = identifier.lowercased()
    guard byteCount > 0,
          byteCount <= defaultCacheMaximumBytes
    else {
      throw PhotosMediaProcessingError.cacheCapacity
    }
    try mediaReservationLedger.createReservation(
      identifier: normalizedIdentifier,
      byteCount: byteCount,
      maximumAggregateByteCount: defaultCacheMaximumBytes,
      reservationDirectory: directory,
      freeSpaceFloorByteCount: defaultFreeSpaceFloorBytes
    )
  }

  private func beginUnknownSizeMediaReservation(identifier: String, at directory: URL) throws {
    try mediaReservationLedger.createReservation(
      identifier: identifier.lowercased(),
      byteCount: 0,
      maximumAggregateByteCount: defaultCacheMaximumBytes,
      reservationDirectory: directory,
      freeSpaceFloorByteCount: defaultFreeSpaceFloorBytes
    )
  }

  private func releaseMediaBytes(identifier: String) {
    mediaReservationLedger.releaseReservation(identifier: identifier.lowercased())
  }

  private func dictionary(_ value: Any?) -> [String: Any] {
    value as? [String: Any] ?? [:]
  }

  private func integerValue(_ value: Any?) -> Int64 {
    (value as? NSNumber)?.int64Value ?? 0
  }

  private func meaningfulText(_ value: Any?) -> String? {
    guard let text = value as? String,
          !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    else { return nil }
    return text
  }

  private func decimalValue(_ value: Any?) -> Double? {
    (value as? NSNumber)?.doubleValue
  }

  private func photoLibraryAccessState(_ status: PHAuthorizationStatus) -> Opentrawl_Photos_Media_PhotoLibraryAccessState {
    switch status {
    case .notDetermined: .notDetermined
    case .restricted: .restricted
    case .denied: .denied
    case .authorized: .authorized
    case .limited: .limited
    @unknown default: .unspecified
    }
  }

  private func photosAccessUnavailableResponse() -> MediaResponse? {
    let status = PHPhotoLibrary.authorizationStatus(for: .readWrite)
    guard status != .authorized, status != .limited else { return nil }
    return unavailableResponse(.photosAccess, "OpenTrawl does not have access to this Apple Photos library.")
  }

  private func imageOrientation(_ rawValue: Int32) -> Opentrawl_Photos_Media_ImageOrientation {
    Opentrawl_Photos_Media_ImageOrientation(rawValue: Int(rawValue)) ?? .unspecified
  }

  private func unavailable(
    _ reason: Opentrawl_Photos_Media_PhotosMediaUnavailableReason,
    _ description: String
  ) -> MediaUnavailable {
    var value = MediaUnavailable()
    value.reason = reason
    value.photoLibraryAccessState = photoLibraryAccessState(PHPhotoLibrary.authorizationStatus(for: .readWrite))
    value.humanDescription = description
    return value
  }

  private func unavailableResponse(
    _ reason: Opentrawl_Photos_Media_PhotosMediaUnavailableReason,
    _ description: String
  ) -> MediaResponse {
    var response = MediaResponse()
    response.unavailable = unavailable(reason, description)
    return response
  }

  private func operationFailure(
    _ kind: Opentrawl_Photos_Media_PhotosMediaOperationFailureKind,
    _ description: String,
    indexedPhotoModificationTime: Date? = nil,
    currentPhotoModificationTime: Date? = nil
  ) -> Opentrawl_Photos_Media_PhotosMediaOperationFailure {
    var failure = Opentrawl_Photos_Media_PhotosMediaOperationFailure()
    failure.kind = kind
    failure.humanDescription = description
    if let indexedPhotoModificationTime {
      failure.indexedPhotoModificationTime = .init(date: indexedPhotoModificationTime)
    }
    if let currentPhotoModificationTime {
      failure.currentPhotoModificationTime = .init(date: currentPhotoModificationTime)
    }
    return failure
  }

  private func operationFailureResponse(
    _ kind: Opentrawl_Photos_Media_PhotosMediaOperationFailureKind,
    _ description: String,
    indexedPhotoModificationTime: Date? = nil,
    currentPhotoModificationTime: Date? = nil
  ) -> MediaResponse {
    var response = MediaResponse()
    response.operationFailure = operationFailure(
      kind,
      description,
      indexedPhotoModificationTime: indexedPhotoModificationTime,
      currentPhotoModificationTime: currentPhotoModificationTime
    )
    return response
  }

  private func immutableOriginalOutcomeResponse(
    request: Opentrawl_Photos_Media_InspectImmutableOriginalImageFactsRequest,
    candidates: [Opentrawl_Photos_Media_PhotoKitOriginalResourceCandidate] = [],
    selectedPhotoKitCandidatePosition: Int? = nil,
    facts: Opentrawl_Photos_Media_ImmutableOriginalImageFacts? = nil,
    operationResponse: MediaResponse? = nil
  ) -> MediaResponse {
    var outcome = Opentrawl_Photos_Media_ImmutableOriginalImageFactsOutcome()
    outcome.request = request
    outcome.photoKitCandidates = candidates
    outcome.completedAt = .init(date: Date())
    if let selectedPhotoKitCandidatePosition {
      outcome.selectedPhotoKitCandidatePosition = Int32(selectedPhotoKitCandidatePosition)
    }
    if let facts {
      outcome.state = .available
      outcome.facts = facts
    } else if let operationResponse {
      switch operationResponse.outcome {
      case .unavailable(let unavailable):
        outcome.state = .unavailable
        outcome.unavailable = unavailable
      case .admissionDeferred(let deferred):
        outcome.state = .failed
        outcome.admissionDeferred = deferred
      case .operationFailure(let failure):
        outcome.state = .failed
        outcome.failure = failure
      default:
        outcome.state = .failed
        outcome.failure = operationFailure(
          .photosProviderFailure,
          "Apple Photos returned no immutable image original outcome."
        )
      }
    } else {
      outcome.state = .failed
      outcome.failure = operationFailure(
        .photosProviderFailure,
        "Apple Photos returned no immutable image original outcome."
      )
    }
    var response = MediaResponse()
    response.immutableOriginalImageFactsOutcome = outcome
    return response
  }

  private func admissionDeferredResponse(
    _ reason: Opentrawl_Photos_Media_PhotosMediaAdmissionDeferralReason,
    _ description: String
  ) -> MediaResponse {
    var deferred = Opentrawl_Photos_Media_PhotosMediaAdmissionDeferred()
    deferred.reason = reason
    deferred.humanDescription = description
    var response = MediaResponse()
    response.admissionDeferred = deferred
    return response
  }

  private func response(for error: PhotosMediaProcessingError) -> MediaResponse {
    switch error {
    case .mediaNotLocal:
      unavailableResponse(.mediaNotLocal, "The image is not available on this Mac and network access is disabled.")
    case .unsupportedImage:
      unavailableResponse(.unsupportedImage, "Apple Photos returned an unsupported image.")
    case .immutableOriginalNotFound:
      unavailableResponse(.immutableOriginalNotFound, "Apple Photos could not provide the indexed immutable image original.")
    case .cacheCapacity:
      admissionDeferredResponse(.cacheCapacity, "OpenTrawl's active Photos media work has reached its 512 MiB disk limit.")
    case .freeSpaceFloor:
      admissionDeferredResponse(.filesystemFreeSpaceFloor, "OpenTrawl deferred Photos media work to preserve free disk space.")
    case .invalidRequestDocument:
      operationFailureResponse(.invalidRequest, "The Photos media request is invalid.")
    case .invalidCacheRoot, .cacheIO:
      operationFailureResponse(.cacheIo, "OpenTrawl could not use its Photos media cache.")
    case .responseTooLarge:
      operationFailureResponse(.ipcIo, "OpenTrawl could not return the Photos media response.")
    case .indexedOriginalChanged:
      operationFailureResponse(.indexedSourceChanged, "The immutable image original changed after OpenTrawl indexed it.")
    case .photosTimeout:
      operationFailureResponse(.photosTimeout, "Apple Photos did not provide the image before the request timed out.")
    case .photosCancelled:
      operationFailureResponse(.photosCancelled, "Apple Photos cancelled the image request.")
    case .photosProviderFailure:
      operationFailureResponse(.photosProviderFailure, "Apple Photos could not provide the image.")
    }
  }
}

private struct CheckedPhotosMediaCache {
  let root: URL
  let maximumBytes: Int64
}

private final class PhotosMediaReservationLedger: @unchecked Sendable {
  private struct ReservationState {
    var totalReservedByteCount: Int64
    var outstandingDiskClaimByteCount: Int64
  }

  private let lock = NSLock()
  private var reservationStateByIdentifier: [String: ReservationState] = [:]

  func containsReservation(identifier: String) -> Bool {
    lock.lock()
    defer { lock.unlock() }
    return reservationStateByIdentifier[identifier.lowercased()] != nil
  }

  func reconstructReservation(
    identifier: String,
    byteCount: Int64,
    maximumAggregateByteCount: Int64
  ) throws {
    let normalizedIdentifier = identifier.lowercased()
    lock.lock()
    defer { lock.unlock() }
    if let existingState = reservationStateByIdentifier[normalizedIdentifier] {
      guard existingState.totalReservedByteCount == byteCount,
            existingState.outstandingDiskClaimByteCount == 0
      else { throw PhotosMediaProcessingError.cacheIO }
      return
    }
    let activeByteCount = reservationStateByIdentifier.values.reduce(Int64(0)) {
      $0 + $1.totalReservedByteCount
    }
    guard byteCount > 0,
          byteCount <= maximumAggregateByteCount,
          activeByteCount <= maximumAggregateByteCount - byteCount
    else { throw PhotosMediaProcessingError.cacheCapacity }
    reservationStateByIdentifier[normalizedIdentifier] = ReservationState(
      totalReservedByteCount: byteCount,
      outstandingDiskClaimByteCount: 0
    )
  }

  func createReservation(
    identifier: String,
    byteCount: Int64,
    maximumAggregateByteCount: Int64,
    reservationDirectory: URL,
    freeSpaceFloorByteCount: Int64
  ) throws {
    let normalizedIdentifier = identifier.lowercased()
    lock.lock()
    defer { lock.unlock() }
    guard reservationStateByIdentifier[normalizedIdentifier] == nil,
          byteCount >= 0
    else { throw PhotosMediaProcessingError.cacheCapacity }
    try checkAdditionalByteCount(
      byteCount,
      maximumAggregateByteCount: maximumAggregateByteCount,
      reservationDirectory: reservationDirectory,
      freeSpaceFloorByteCount: freeSpaceFloorByteCount
    )
    reservationStateByIdentifier[normalizedIdentifier] = ReservationState(
      totalReservedByteCount: byteCount,
      outstandingDiskClaimByteCount: byteCount
    )
  }

  func increaseReservation(
    identifier: String,
    additionalByteCount: Int64,
    maximumAggregateByteCount: Int64,
    reservationDirectory: URL,
    freeSpaceFloorByteCount: Int64
  ) throws {
    let normalizedIdentifier = identifier.lowercased()
    lock.lock()
    defer { lock.unlock() }
    guard var currentState = reservationStateByIdentifier[normalizedIdentifier],
          additionalByteCount > 0,
          currentState.totalReservedByteCount <= maximumAggregateByteCount - additionalByteCount
    else { throw PhotosMediaProcessingError.cacheCapacity }
    try checkAdditionalByteCount(
      additionalByteCount,
      maximumAggregateByteCount: maximumAggregateByteCount,
      reservationDirectory: reservationDirectory,
      freeSpaceFloorByteCount: freeSpaceFloorByteCount
    )
    currentState.totalReservedByteCount += additionalByteCount
    currentState.outstandingDiskClaimByteCount += additionalByteCount
    reservationStateByIdentifier[normalizedIdentifier] = currentState
  }

  func recordMaterializedBytes(identifier: String, byteCount: Int64) throws {
    let normalizedIdentifier = identifier.lowercased()
    lock.lock()
    defer { lock.unlock() }
    guard var currentState = reservationStateByIdentifier[normalizedIdentifier],
          byteCount > 0,
          byteCount <= currentState.outstandingDiskClaimByteCount
    else { throw PhotosMediaProcessingError.cacheIO }
    currentState.outstandingDiskClaimByteCount -= byteCount
    reservationStateByIdentifier[normalizedIdentifier] = currentState
  }

  func releaseReservation(identifier: String) {
    lock.lock()
    reservationStateByIdentifier.removeValue(forKey: identifier.lowercased())
    lock.unlock()
  }

  private func checkAdditionalByteCount(
    _ additionalByteCount: Int64,
    maximumAggregateByteCount: Int64,
    reservationDirectory: URL,
    freeSpaceFloorByteCount: Int64
  ) throws {
    let activeByteCount = reservationStateByIdentifier.values.reduce(Int64(0)) {
      $0 + $1.totalReservedByteCount
    }
    guard additionalByteCount <= maximumAggregateByteCount,
          activeByteCount <= maximumAggregateByteCount - additionalByteCount
    else { throw PhotosMediaProcessingError.cacheCapacity }
    let availableCapacity: Int64
    do {
      guard let available = try reservationDirectory.resourceValues(
        forKeys: [.volumeAvailableCapacityForImportantUsageKey]
      ).volumeAvailableCapacityForImportantUsage else {
        throw PhotosMediaProcessingError.freeSpaceFloor
      }
      availableCapacity = Int64(available)
    } catch let error as PhotosMediaProcessingError {
      throw error
    } catch {
      throw PhotosMediaProcessingError.cacheIO
    }
    let outstandingDiskClaimByteCount = reservationStateByIdentifier.values.reduce(Int64(0)) {
      $0 + $1.outstandingDiskClaimByteCount
    }
    guard availableCapacity >= freeSpaceFloorByteCount,
          outstandingDiskClaimByteCount <= availableCapacity - freeSpaceFloorByteCount,
          additionalByteCount <= availableCapacity - freeSpaceFloorByteCount - outstandingDiskClaimByteCount
    else { throw PhotosMediaProcessingError.freeSpaceFloor }
  }
}

private struct CheckedImageFile {
  var data: Data
  let byteCount: Int64
  let sha256: Data
  var uniformTypeIdentifier: String
  var orientation: Int32
  let pixelWidth: Int64
  let pixelHeight: Int64
}

private struct CurrentRenderedStillDerivation {
  let output: CheckedImageFile
  let sourcePixelWidth: Int64
  let sourcePixelHeight: Int64
  let sourceOrientation: Int32
  let jpegMaximumPixelDimension: Int64
}

private struct RenderedPhotoKitResult: Sendable {
  let data: Data?
  let orientation: Int32
  let isInCloud: Bool
}

private enum RenderedPhotoKitOutcome: Sendable {
  case completed(RenderedPhotoKitResult)
  case networkAccessRequired
  case providerFailure
  case cancelled
  case timedOut
}

private enum OriginalResourcePhotoKitOutcome: Sendable {
  case completed
  case networkAccessRequired
  case resourceMissing
  case providerFailure
  case cancelled
  case timedOut
}

private enum PhotoKitCallbackFactory {
  nonisolated static func renderedStillResultHandler(
    completion: OneShotContinuation<RenderedPhotoKitOutcome>
  ) -> @Sendable (Data?, String?, CGImagePropertyOrientation, [AnyHashable: Any]?) -> Void {
    { data, _, orientation, callbackInformation in
      if (callbackInformation?[PHImageCancelledKey] as? Bool) == true {
        completion.resume(returning: .cancelled)
        return
      }
      if let error = callbackInformation?[PHImageErrorKey] as? Error {
        completion.resume(returning: isPhotoKitNetworkAccessRequired(error) ? .networkAccessRequired : .providerFailure)
        return
      }
      if (callbackInformation?[PHImageResultIsDegradedKey] as? Bool) == true {
        return
      }
      completion.resume(returning: .completed(RenderedPhotoKitResult(
        data: data,
        orientation: Int32(orientation.rawValue),
        isInCloud: (callbackInformation?[PHImageResultIsInCloudKey] as? Bool) == true
      )))
    }
  }

  nonisolated static func originalResourceDataHandler(
    writer: BoundedOriginalResourceWriter,
    completion: OneShotContinuation<OriginalResourcePhotoKitOutcome>,
    cancellation: PhotosResourceRequestCancellation
  ) -> @Sendable (Data) -> Void {
    { chunk in
      if !writer.append(chunk) {
        completion.resume(returning: .providerFailure)
        cancellation.cancel()
      }
    }
  }

  nonisolated static func originalResourceCompletionHandler(
    completion: OneShotContinuation<OriginalResourcePhotoKitOutcome>
  ) -> @Sendable (Error?) -> Void {
    { error in
      guard let error else {
        completion.resume(returning: .completed)
        return
      }
      let photosError = error as NSError
      guard photosError.domain == PHPhotosErrorDomain else {
        completion.resume(returning: .providerFailure)
        return
      }
      switch photosError.code {
      case PHPhotosError.networkAccessRequired.rawValue:
        completion.resume(returning: .networkAccessRequired)
      case PHPhotosError.missingResource.rawValue, PHPhotosError.identifierNotFound.rawValue:
        completion.resume(returning: .resourceMissing)
      case PHPhotosError.userCancelled.rawValue:
        completion.resume(returning: .cancelled)
      default:
        completion.resume(returning: .providerFailure)
      }
    }
  }

  nonisolated private static func isPhotoKitNetworkAccessRequired(_ error: Error) -> Bool {
    let photosError = error as NSError
    return photosError.domain == PHPhotosErrorDomain
      && photosError.code == PHPhotosError.networkAccessRequired.rawValue
  }

  nonisolated static func scheduleRenderedStillTimeout(
    after timeout: TimeInterval,
    completion: OneShotContinuation<RenderedPhotoKitOutcome>,
    cancellation: PhotosImageRequestCancellation
  ) {
    DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + timeout) {
      if completion.resume(returning: .timedOut) {
        cancellation.cancel()
      }
    }
  }

  nonisolated static func scheduleOriginalResourceTimeout(
    after timeout: TimeInterval,
    completion: OneShotContinuation<OriginalResourcePhotoKitOutcome>,
    cancellation: PhotosResourceRequestCancellation
  ) {
    DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + timeout) {
      if completion.resume(returning: .timedOut) {
        cancellation.cancel()
      }
    }
  }
}

private final class OneShotContinuation<Output: Sendable>: @unchecked Sendable {
  private let lock = NSLock()
  private var continuation: CheckedContinuation<Output, Never>?
  private var completedOutput: Output?
  private var hasCompleted = false

  func install(_ continuation: CheckedContinuation<Output, Never>) {
    lock.lock()
    if hasCompleted {
      let completedOutput = completedOutput
      lock.unlock()
      continuation.resume(returning: completedOutput!)
      return
    }
    self.continuation = continuation
    lock.unlock()
  }

  @discardableResult
  func resume(returning output: Output) -> Bool {
    lock.lock()
    guard !hasCompleted else {
      lock.unlock()
      return false
    }
    hasCompleted = true
    completedOutput = output
    let continuation = continuation
    self.continuation = nil
    lock.unlock()
    continuation?.resume(returning: output)
    return true
  }
}

private final class PhotosImageRequestCancellation: @unchecked Sendable {
  private let lock = NSLock()
  private let manager: PHImageManager
  private var requestIdentifier: PHImageRequestID?
  private var cancellationRequested = false

  init(manager: PHImageManager) {
    self.manager = manager
  }

  func setRequestIdentifier(_ requestIdentifier: PHImageRequestID) {
    lock.lock()
    self.requestIdentifier = requestIdentifier
    let shouldCancel = cancellationRequested
    lock.unlock()
    if shouldCancel { manager.cancelImageRequest(requestIdentifier) }
  }

  func cancel() {
    lock.lock()
    cancellationRequested = true
    let requestIdentifier = requestIdentifier
    lock.unlock()
    if let requestIdentifier { manager.cancelImageRequest(requestIdentifier) }
  }
}

private final class BoundedOriginalResourceWriter: @unchecked Sendable {
  private let lock = NSLock()
  private let file: FileHandle
  private let expectedByteCount: Int64
  private let maximumByteCount: Int64
  private let reserveAdditionalBytes: (@Sendable (Int64) throws -> Void)?
  private let recordMaterializedBytes: @Sendable (Int64) throws -> Void
  private var byteCount: Int64 = 0
  private var closed = false
  private(set) var failure: PhotosMediaProcessingError?

  init(
    destinationURL: URL,
    expectedByteCount: Int64,
    maximumByteCount: Int64,
    reserveAdditionalBytes: (@Sendable (Int64) throws -> Void)?,
    recordMaterializedBytes: @escaping @Sendable (Int64) throws -> Void
  ) throws {
    file = try FileHandle(forWritingTo: destinationURL)
    self.expectedByteCount = expectedByteCount
    self.maximumByteCount = maximumByteCount
    self.reserveAdditionalBytes = reserveAdditionalBytes
    self.recordMaterializedBytes = recordMaterializedBytes
  }

  func append(_ data: Data) -> Bool {
    lock.lock()
    defer { lock.unlock() }
    guard failure == nil, !closed else { return false }
    let nextByteCount = byteCount + Int64(data.count)
    guard (expectedByteCount == 0 || nextByteCount <= expectedByteCount),
          nextByteCount <= maximumByteCount else {
      failure = expectedByteCount > 0 ? .indexedOriginalChanged : .cacheCapacity
      return false
    }
    do {
      try reserveAdditionalBytes?(Int64(data.count))
      try file.write(contentsOf: data)
      try recordMaterializedBytes(Int64(data.count))
      byteCount = nextByteCount
      return true
    } catch let error as PhotosMediaProcessingError {
      failure = error
      return false
    } catch {
      failure = .cacheIO
      return false
    }
  }

  func close() throws {
    lock.lock()
    defer { lock.unlock() }
    guard !closed else { return }
    closed = true
    try file.close()
  }

  func failureSnapshot() -> PhotosMediaProcessingError? {
    lock.lock()
    defer { lock.unlock() }
    return failure
  }

  func byteCountSnapshot() -> Int64 {
    lock.lock()
    defer { lock.unlock() }
    return byteCount
  }
}

private final class PhotosResourceRequestCancellation: @unchecked Sendable {
  private let lock = NSLock()
  private let manager: PHAssetResourceManager
  private var requestIdentifier: PHAssetResourceDataRequestID?
  private var cancellationRequested = false

  init(manager: PHAssetResourceManager) {
    self.manager = manager
  }

  func setRequestIdentifier(_ requestIdentifier: PHAssetResourceDataRequestID) {
    lock.lock()
    self.requestIdentifier = requestIdentifier
    let shouldCancel = cancellationRequested
    lock.unlock()
    if shouldCancel { manager.cancelDataRequest(requestIdentifier) }
  }

  func cancel() {
    lock.lock()
    cancellationRequested = true
    let requestIdentifier = requestIdentifier
    lock.unlock()
    if let requestIdentifier { manager.cancelDataRequest(requestIdentifier) }
  }
}

private enum PhotosMediaProcessingError: Error {
  case invalidRequestDocument
  case invalidCacheRoot
  case responseTooLarge
  case mediaNotLocal
  case unsupportedImage
  case immutableOriginalNotFound
  case cacheCapacity
  case freeSpaceFloor
  case photosTimeout
  case photosCancelled
  case photosProviderFailure
  case cacheIO
  case indexedOriginalChanged
}
