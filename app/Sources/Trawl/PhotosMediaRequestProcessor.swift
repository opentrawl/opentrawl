import CryptoKit
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
  private var recoveredIncompleteOriginalReads = false

  func process(requestDocumentURL: URL) async {
    let responseDocumentURL = Self.responseDocumentURL(for: requestDocumentURL)
    do {
      let ipcDirectory = try checkedIPCDirectory(containing: requestDocumentURL)
      let request = try readRequest(from: requestDocumentURL)
      let response = await perform(request, ipcDirectory: ipcDirectory)
      try writeResponse(response, to: responseDocumentURL)
    } catch {
      guard (try? checkedIPCDirectory(containing: requestDocumentURL)) != nil else {
        return
      }
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
    guard let asset = photoAsset(localIdentifier: request.photoAssetLocalIdentifier) else {
      return unavailableResponse(.assetNotFound, "OpenTrawl could not find this photo in Apple Photos.")
    }
    guard asset.mediaType == .image else {
      return unavailableResponse(.notAnImage, "This Apple Photos asset is not an image.")
    }
    guard let original = immutableOriginalResource(for: asset) else {
      return unavailableResponse(.immutableOriginalNotFound, "Apple Photos did not expose an immutable image original.")
    }
    var readiness = Opentrawl_Photos_Media_PhotoAssetReadiness()
    readiness.photoAssetLocalIdentifier = asset.localIdentifier
    readiness.pixelWidth = UInt64(asset.pixelWidth)
    readiness.pixelHeight = UInt64(asset.pixelHeight)
    if let creationDate = asset.creationDate {
      readiness.creationTime = .init(date: creationDate)
    }
    if let modificationDate = asset.modificationDate {
      readiness.modificationTime = .init(date: modificationDate)
    }
    readiness.immutableOriginalFilename = original.originalFilename
    readiness.immutableOriginalUniformTypeIdentifier = original.uniformTypeIdentifier
    var response = MediaResponse()
    response.photoAssetReadiness = readiness
    return response
  }

  private func acquireCurrentRenderedStill(
    _ request: Opentrawl_Photos_Media_AcquireCurrentRenderedStillRequest,
    ipcDirectory: URL
  ) async -> MediaResponse {
    guard !request.sourcePhotosLibraryIdentifier.isEmpty,
          request.hasFreshness,
          let asset = photoAsset(localIdentifier: request.photoAssetLocalIdentifier)
    else {
      return operationFailureResponse(.invalidRequest, "The current rendered image request is incomplete.")
    }
    guard asset.mediaType == .image else {
      return unavailableResponse(.notAnImage, "This Apple Photos asset is not an image.")
    }
    if case .expectedPhotoModificationTime(let expected)? = request.freshness.freshness,
       let actual = asset.modificationDate,
       abs(actual.timeIntervalSince(expected.date)) >= 0.001
    {
      return operationFailureResponse(.invalidRequest, "The photo changed after OpenTrawl indexed it.")
    }
    do {
      let rendered = try await currentRenderedStill(
        for: asset,
        allowNetwork: request.allowIcloudNetworkAccess
      )
      let leaseIdentifier = UUID().uuidString.lowercased()
      let leaseURL = ipcDirectory.appendingPathComponent(leaseIdentifier).appendingPathExtension("image")
      try admit(byteCount: rendered.byteCount, at: ipcDirectory, maximumBytes: defaultCacheMaximumBytes,
                freeSpaceFloorBytes: defaultFreeSpaceFloorBytes)
      try rendered.data.write(to: leaseURL, options: [.atomic])
      try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: leaseURL.path)
      var lease = Opentrawl_Photos_Media_CurrentRenderedStillLease()
      lease.leaseIdentifier = leaseIdentifier
      lease.checkedFilePath = leaseURL.path
      lease.byteCount = UInt64(rendered.byteCount)
      lease.sha256 = rendered.sha256
      lease.uniformTypeIdentifier = rendered.uniformTypeIdentifier
      lease.imageOrientation = imageOrientation(rendered.orientation)
      lease.pixelWidth = UInt64(rendered.pixelWidth)
      lease.pixelHeight = UInt64(rendered.pixelHeight)
      var response = MediaResponse()
      response.currentRenderedStillLease = lease
      return response
    } catch let error as PhotosMediaProcessingError {
      return response(for: error)
    } catch {
      return operationFailureResponse(.photokit, "Apple Photos could not provide the current rendered image.")
    }
  }

  private func inspectImmutableOriginalImageFacts(
    _ request: Opentrawl_Photos_Media_InspectImmutableOriginalImageFactsRequest
  ) async -> MediaResponse {
    guard let asset = photoAsset(localIdentifier: request.photoAssetLocalIdentifier) else {
      return unavailableResponse(.assetNotFound, "OpenTrawl could not find this photo in Apple Photos.")
    }
    guard asset.mediaType == .image else {
      return unavailableResponse(.notAnImage, "This Apple Photos asset is not an image.")
    }
    guard let resource = immutableOriginalResource(
      for: asset,
      expectedFilename: request.expectedImmutableOriginalFilename,
      expectedUniformTypeIdentifier: request.expectedImmutableOriginalUniformTypeIdentifier
    ) else {
      return unavailableResponse(.immutableOriginalNotFound, "Apple Photos did not expose the indexed immutable image original.")
    }
    do {
      let cache = try checkedCache()
      if request.expectedImmutableOriginalByteCount > 0 {
        try admit(
          byteCount: Int64(request.expectedImmutableOriginalByteCount),
          at: cache.root,
          maximumBytes: cache.maximumBytes,
          freeSpaceFloorBytes: cache.freeSpaceFloorBytes
        )
      }
      let temporaryURL = cache.root.appendingPathComponent(".\(UUID().uuidString).original-reading")
      defer { try? fileManager.removeItem(at: temporaryURL) }
      try await writeOriginalResource(
        resource,
        to: temporaryURL,
        allowNetwork: request.allowIcloudNetworkAccess,
        cache: cache,
        expectedByteCount: Int64(request.expectedImmutableOriginalByteCount)
      )
      let facts = try imageFacts(at: temporaryURL, uniformTypeIdentifier: resource.uniformTypeIdentifier)
      var response = MediaResponse()
      response.immutableOriginalImageFacts = facts
      return response
    } catch let error as PhotosMediaProcessingError {
      return response(for: error)
    } catch {
      return operationFailureResponse(.photokit, "Apple Photos could not inspect the immutable image original.")
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
    try? fileManager.removeItem(at: leaseURL)
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

  private func immutableOriginalResource(for asset: PHAsset) -> PHAssetResource? {
    let originals = PHAssetResource.assetResources(for: asset).filter { $0.type == .photo }
    return originals.count == 1 ? originals[0] : nil
  }

  private func immutableOriginalResource(
    for asset: PHAsset,
    expectedFilename: String,
    expectedUniformTypeIdentifier: String
  ) -> PHAssetResource? {
    guard !expectedFilename.isEmpty, !expectedUniformTypeIdentifier.isEmpty else { return nil }
    return PHAssetResource.assetResources(for: asset).first {
      $0.type == .photo
        && $0.originalFilename == expectedFilename
        && $0.uniformTypeIdentifier == expectedUniformTypeIdentifier
    }
  }

  private func currentRenderedStill(
    for asset: PHAsset,
    allowNetwork: Bool
  ) async throws -> CheckedImageFile {
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
      throw allowNetwork ? PhotosMediaProcessingError.photoKitFailure : PhotosMediaProcessingError.mediaNotLocal
    case .cancelled, .providerFailure, .timedOut:
      throw PhotosMediaProcessingError.photoKitFailure
    }
    if result.isInCloud, result.data == nil {
      throw allowNetwork ? PhotosMediaProcessingError.photoKitFailure : PhotosMediaProcessingError.mediaNotLocal
    }
    guard let data = result.data, !data.isEmpty else {
      throw PhotosMediaProcessingError.photoKitFailure
    }
    var checked = try checkedImageData(data)
    checked.data = data
    checked.uniformTypeIdentifier = result.uniformTypeIdentifier ?? checked.uniformTypeIdentifier
    checked.orientation = result.orientation
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
    cache: CheckedPhotosMediaCache,
    expectedByteCount: Int64
  ) async throws {
    if expectedByteCount > 0 {
      try admit(byteCount: expectedByteCount, at: cache.root, maximumBytes: cache.maximumBytes,
                freeSpaceFloorBytes: cache.freeSpaceFloorBytes)
    }
    guard fileManager.createFile(atPath: destinationURL.path, contents: nil) else {
      throw PhotosMediaProcessingError.cacheIO
    }
    let writer = try BoundedOriginalResourceWriter(
      destinationURL: destinationURL,
      expectedByteCount: expectedByteCount,
      maximumByteCount: cache.maximumBytes
    )
    let completionError = await streamOriginalResource(
      resource,
      allowNetwork: allowNetwork,
      writer: writer,
      timeout: photoKitMediaRequestTimeout
    )
    try writer.close()
    if let writerError = writer.failureSnapshot() { throw writerError }
    switch completionError {
    case .completed:
      break
    case .networkAccessRequired:
      throw allowNetwork ? PhotosMediaProcessingError.photoKitFailure : PhotosMediaProcessingError.mediaNotLocal
    case .resourceMissing:
      throw PhotosMediaProcessingError.immutableOriginalNotFound
    case .cancelled, .providerFailure, .timedOut:
      throw PhotosMediaProcessingError.photoKitFailure
    }
    guard expectedByteCount == 0 || writer.byteCountSnapshot() == expectedByteCount else {
      throw PhotosMediaProcessingError.indexedOriginalChanged
    }
    let byteCount = try destinationURL.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
    guard byteCount > 0 else { throw PhotosMediaProcessingError.mediaNotLocal }
    try admit(byteCount: Int64(byteCount), at: cache.root, maximumBytes: cache.maximumBytes,
              freeSpaceFloorBytes: cache.freeSpaceFloorBytes)
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

  private func checkedCache() throws -> CheckedPhotosMediaCache {
    let maximumBytes = defaultCacheMaximumBytes
    let freeSpaceFloor = defaultFreeSpaceFloorBytes
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
    if !recoveredIncompleteOriginalReads {
      for candidate in try fileManager.contentsOfDirectory(
        at: root,
        includingPropertiesForKeys: nil,
        options: [.skipsSubdirectoryDescendants]
      ) where candidate.lastPathComponent.hasPrefix(".")
        && candidate.lastPathComponent.hasSuffix(".original-reading")
      {
        try fileManager.removeItem(at: candidate)
      }
      recoveredIncompleteOriginalReads = true
    }
    return CheckedPhotosMediaCache(root: root, maximumBytes: maximumBytes, freeSpaceFloorBytes: freeSpaceFloor)
  }

  private func checkedIPCDirectory(containing requestDocumentURL: URL) throws -> URL {
    let directory = requestDocumentURL.deletingLastPathComponent().resolvingSymlinksInPath().standardizedFileURL
    let temporaryDirectory = FileManager.default.temporaryDirectory.resolvingSymlinksInPath().standardizedFileURL
    guard directory.lastPathComponent.hasPrefix("opentrawl-photos-media-"),
          directory.deletingLastPathComponent().standardizedFileURL == temporaryDirectory,
          requestDocumentURL.lastPathComponent == "request.opentrawl-photos-media-request"
    else { throw PhotosMediaProcessingError.invalidRequestDocument }
    let attributes = try fileManager.attributesOfItem(atPath: directory.path)
    guard (attributes[.ownerAccountID] as? NSNumber)?.uint32Value == getuid(),
          ((attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0) & 0o777 == 0o700
    else { throw PhotosMediaProcessingError.invalidRequestDocument }
    return directory
  }

  private func admit(
    byteCount: Int64,
    at directory: URL,
    maximumBytes: Int64,
    freeSpaceFloorBytes: Int64
  ) throws {
    guard byteCount <= maximumBytes else { throw PhotosMediaProcessingError.cacheCapacity }
    let values = try directory.resourceValues(forKeys: [.volumeAvailableCapacityForImportantUsageKey])
    if let available = values.volumeAvailableCapacityForImportantUsage,
       Int64(available) - byteCount < freeSpaceFloorBytes
    {
      throw PhotosMediaProcessingError.freeSpaceFloor
    }
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
    _ description: String
  ) -> Opentrawl_Photos_Media_PhotosMediaOperationFailure {
    var failure = Opentrawl_Photos_Media_PhotosMediaOperationFailure()
    failure.kind = kind
    failure.humanDescription = description
    return failure
  }

  private func operationFailureResponse(
    _ kind: Opentrawl_Photos_Media_PhotosMediaOperationFailureKind,
    _ description: String
  ) -> MediaResponse {
    var response = MediaResponse()
    response.operationFailure = operationFailure(kind, description)
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
      admissionDeferredResponse(.cacheCapacity, "The image is larger than OpenTrawl's Photos media cache.")
    case .freeSpaceFloor:
      admissionDeferredResponse(.filesystemFreeSpaceFloor, "OpenTrawl deferred Photos media work to preserve free disk space.")
    case .invalidCacheRoot, .invalidRequestDocument, .responseTooLarge:
      operationFailureResponse(.invalidRequest, "The Photos media request is invalid.")
    case .cacheIO:
      operationFailureResponse(.cacheIo, "OpenTrawl could not use its Photos media cache.")
    case .indexedOriginalChanged:
      operationFailureResponse(.indexedSourceChanged, "The immutable image original changed after OpenTrawl indexed it.")
    case .photoKitFailure:
      operationFailureResponse(.photokit, "Apple Photos could not provide the image.")
    }
  }
}

private struct CheckedPhotosMediaCache {
  let root: URL
  let maximumBytes: Int64
  let freeSpaceFloorBytes: Int64
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

private struct RenderedPhotoKitResult: Sendable {
  let data: Data?
  let uniformTypeIdentifier: String?
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
    { data, uniformTypeIdentifier, orientation, callbackInformation in
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
        uniformTypeIdentifier: uniformTypeIdentifier,
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
  private var byteCount: Int64 = 0
  private var closed = false
  private(set) var failure: PhotosMediaProcessingError?

  init(destinationURL: URL, expectedByteCount: Int64, maximumByteCount: Int64) throws {
    file = try FileHandle(forWritingTo: destinationURL)
    self.expectedByteCount = expectedByteCount
    self.maximumByteCount = maximumByteCount
  }

  func append(_ data: Data) -> Bool {
    lock.lock()
    defer { lock.unlock() }
    guard failure == nil, !closed else { return false }
    let nextByteCount = byteCount + Int64(data.count)
    guard (expectedByteCount == 0 || nextByteCount <= expectedByteCount),
          nextByteCount <= maximumByteCount else {
      failure = .cacheCapacity
      return false
    }
    do {
      try file.write(contentsOf: data)
      byteCount = nextByteCount
      return true
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
  case photoKitFailure
  case cacheIO
  case indexedOriginalChanged
}
