import Foundation
import TrawlClient

enum OpenedPhotoLayout {
  static let sideBySideInspectorMinimumWidth: CGFloat = 820
  static let inspectorWidth: CGFloat = 310
  static let stagePadding: CGFloat = 16
  static let topActionBarEstimatedHeight: CGFloat = 42
  static let zoomSheetMinimumSize = CGSize(width: 900, height: 680)
  static let zoomSheetIdealSize = CGSize(width: 1_120, height: 820)

  static func usesSideBySideInspector(forPaneWidth paneWidth: CGFloat) -> Bool {
    paneWidth >= sideBySideInspectorMinimumWidth
  }

  static func verticalPhotoStageHeight(forAvailableHeight availableHeight: CGFloat) -> CGFloat {
    guard availableHeight > 0 else { return 360 }
    let preferredHeight = availableHeight * 0.68
    let minimumHeight = min(280, availableHeight * 0.65)
    let maximumHeight = max(minimumHeight, availableHeight - 140)
    return floor(min(max(preferredHeight, minimumHeight), maximumHeight))
  }
}

struct OpenedPhotoDetailField: Equatable, Identifiable {
  let id: String
  let label: String
  let value: String
}

struct OpenedPhotoVisibleTextGroup: Equatable, Identifiable {
  let id: String
  let text: String
  let englishRendering: String?
}

/// Structured, human-first presentation of an opened Photos record, parsed from
/// the shared `TrawlerSpecificCommandDetailPresentation` wire contract.
struct OpenedPhotoPresentation: Equatable {
  let mediaKind: String
  let headerPrimaryText: String
  let headerDateText: String?
  let isFavourite: Bool
  let summary: String?
  let title: String?
  let description: String?
  let caption: String?
  let keyFacts: [OpenedPhotoDetailField]
  let recognisedText: String?
  let visibleTextGroups: [OpenedPhotoVisibleTextGroup]
  let technicalDetails: [OpenedPhotoDetailField]
  let footerText: String?
  let localCachedPictureFilePath: String?
  let pictureStatusFieldText: String?

  private static let failedFetchPrefix = "Fetching the picture failed:"
  private static let remoteEndpointPlaceholder = "Available via remote photo endpoint."
  private static let notFetchedYetText = "Not fetched yet."
  private static let evictedFromCacheText = "Fetched; no longer in the picture cache."
  private static let placeNotRecordedText = "Place not recorded"
  private static let placeHierarchySeparator = " › "

  var hasNarrativeContent: Bool {
    summary != nil || title != nil || description != nil || caption != nil
  }

  var copyableRecognisedText: String? {
    if let recognisedText, !recognisedText.isEmpty {
      return recognisedText
    }
    let joinedVisibleText = visibleTextGroups
      .map(\.text)
      .filter { !$0.isEmpty }
      .joined(separator: "\n\n")
    return joinedVisibleText.isEmpty ? nil : joinedVisibleText
  }

  var hasRecognisedTextSection: Bool {
    recognisedText != nil || !visibleTextGroups.isEmpty
  }

  init(
    detailPresentation: TrawlerSpecificCommandDetailPresentation,
    locale: Locale = .autoupdatingCurrent
  ) {
    let trimmedKind = detailPresentation.detailDisplayName.trimmingCharacters(
      in: .whitespacesAndNewlines
    )
    let resolvedKind = trimmedKind.isEmpty ? "Photo" : trimmedKind
    self.mediaKind = resolvedKind

    let parsed = Self.parseFields(
      detailPresentation.fieldsInDisplayOrder,
      body: detailPresentation.body,
      subtitle: detailPresentation.detailSubtitle
    )

    self.summary = parsed.summary
    self.title = parsed.title
    self.description = parsed.description
    self.caption = parsed.caption
    self.isFavourite = parsed.favourite?.caseInsensitiveCompare("Yes") == .orderedSame
    self.recognisedText = parsed.recognisedText
    self.visibleTextGroups = parsed.visibleTextGroups
    self.footerText = detailPresentation.detailFooterText
    self.pictureStatusFieldText = parsed.pictureFieldText
    if let pictureText = parsed.pictureFieldText, pictureText.hasPrefix("/") {
      self.localCachedPictureFilePath = pictureText
    } else {
      self.localCachedPictureFilePath = nil
    }

    let formattedDate = parsed.capturedRaw.map {
      Self.formatCapturedDate($0, locale: locale)
    }
    self.headerDateText = formattedDate

    let concisePlace = Self.concisePlaceName(
      placeShown: parsed.placeShown,
      cameraPlaceRaw: parsed.cameraPlaceRaw
    )
    self.headerPrimaryText = concisePlace ?? resolvedKind

    self.keyFacts = Self.buildKeyFacts(
      formattedDate: formattedDate,
      parsed: parsed
    )
    self.technicalDetails = Self.buildTechnicalDetails(
      mediaKind: resolvedKind,
      parsed: parsed
    )
  }

  static func localCachedPictureFilePath(
    from fields: [TrawlerSpecificCommandDetailPresentationField]
  ) -> String? {
    guard let text = pictureFieldText(from: fields), text.hasPrefix("/") else {
      return nil
    }
    return text
  }

  static func pictureFieldText(
    from fields: [TrawlerSpecificCommandDetailPresentationField]
  ) -> String? {
    for field in fields where field.fieldDisplayName == "Picture" {
      if let text = Self.trimmedTextValue(of: field) {
        return text
      }
    }
    return nil
  }

  static func unavailableReason(
    fromPictureFieldText rawText: String?,
    isRemoteArchive: Bool = false
  ) -> String? {
    guard let trimmed = rawText?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty
    else {
      return nil
    }
    if trimmed.hasPrefix("/") || trimmed == remoteEndpointPlaceholder {
      return nil
    }
    if isRemoteArchive, trimmed == notFetchedYetText || trimmed == evictedFromCacheText {
      return nil
    }
    if trimmed.hasPrefix(failedFetchPrefix) {
      let detail = trimmed.dropFirst(failedFetchPrefix.count).trimmingCharacters(
        in: .whitespacesAndNewlines
      )
      return detail.isEmpty ? "Fetching the picture failed." : detail
    }
    return trimmed
  }

  static func cleanFieldLabel(_ rawLabel: String) -> String {
    let trimmed = rawLabel.trimmingCharacters(in: .whitespacesAndNewlines)
    let stripped =
      trimmed
      .replacingOccurrences(of: " (Apple)", with: "")
      .replacingOccurrences(of: " (standard coordinates)", with: "")
      .trimmingCharacters(in: .whitespacesAndNewlines)
    return stripped.isEmpty ? trimmed : stripped
  }

  /// Formats `trawlkit/render`'s opened-record timestamp (such as
  /// `"2016-11-09 14:46 +00:00 (2016-11-09 14:46 UTC)"` or `"2026-11-25 00:06 UTC"`)
  /// into a concise human date/time preserving the photo's local capture wall clock.
  static func formatCapturedDate(
    _ rawCapturedText: String,
    locale: Locale = .autoupdatingCurrent
  ) -> String {
    let trimmed = rawCapturedText.trimmingCharacters(in: .whitespacesAndNewlines)
    guard trimmed.count >= 16 else { return trimmed }

    let chars = Array(trimmed.prefix(16))
    guard chars[4] == "-", chars[7] == "-", chars[10] == " ", chars[13] == ":",
      let year = Int(String(chars[0...3])),
      let month = Int(String(chars[5...6])),
      let day = Int(String(chars[8...9])),
      let hour = Int(String(chars[11...12])),
      let minute = Int(String(chars[14...15])),
      (1...12).contains(month),
      (1...31).contains(day),
      (0...23).contains(hour),
      (0...59).contains(minute)
    else {
      return trimmed
    }

    var calendar = Calendar(identifier: .gregorian)
    let gmtTimeZone = TimeZone(secondsFromGMT: 0) ?? .gmt
    calendar.timeZone = gmtTimeZone

    var components = DateComponents()
    components.calendar = calendar
    components.timeZone = gmtTimeZone
    components.year = year
    components.month = month
    components.day = day
    components.hour = hour
    components.minute = minute

    guard let date = calendar.date(from: components) else {
      return trimmed
    }

    let dateStyle = Date.FormatStyle(
      locale: locale,
      calendar: calendar,
      timeZone: gmtTimeZone
    )
    .day(.defaultDigits)
    .month(.abbreviated)
    .year(.defaultDigits)

    let datePart = dateStyle.format(date)
    let hourPart = hour < 10 ? "0\(hour)" : "\(hour)"
    let minutePart = minute < 10 ? "0\(minute)" : "\(minute)"
    return "\(datePart), \(hourPart):\(minutePart)"
  }

  static func formatCompactCameraSummary(
    camera: String?,
    settings: String?,
    dimensions: String?,
    duration: String?
  ) -> String? {
    var components: [String] = []

    if let camera = cleanCameraName(camera) {
      components.append(camera)
    }

    if let compactSettings = compactCameraSettings(settings) {
      components.append(compactSettings)
    }

    if let compactDimensions = compactPixelDimensions(dimensions) {
      components.append(compactDimensions)
    }

    if let duration = duration?.trimmingCharacters(in: .whitespacesAndNewlines), !duration.isEmpty {
      components.append(duration)
    }

    return components.isEmpty ? nil : components.joined(separator: " · ")
  }

  private struct ParsedFields {
    var summary: String?
    var title: String?
    var description: String?
    var caption: String?
    var placeShown: String?
    var capturedRaw: String?
    var captureZone: String?
    var cameraPlaceRaw: String?
    var coordinates: String?
    var altitude: String?
    var gpsAccuracy: String?
    var water: String?
    var island: String?
    var camera: String?
    var lens: String?
    var settings: String?
    var people: String?
    var albums: String?
    var favourite: String?
    var library: String?
    var contributor: String?
    var editingState: String?
    var moment: String?
    var dimensions: String?
    var duration: String?
    var trips: [String] = []
    var recognisedText: String?
    var visibleTextGroups: [OpenedPhotoVisibleTextGroup] = []
    var cardNote: String?
    var pictureFieldText: String?
    var extraTechnicalFields: [OpenedPhotoDetailField] = []
  }

  private static func parseFields(
    _ fields: [TrawlerSpecificCommandDetailPresentationField],
    body: TrawlerSpecificCommandDetailPresentationBody?,
    subtitle: String?
  ) -> ParsedFields {
    var parsed = ParsedFields()
    var pendingVisibleTextIndex: Int?

    for (index, field) in fields.enumerated() {
      guard let text = trimmedTextValue(of: field) else { continue }
      let name = field.fieldDisplayName

      if name == "In English", let groupIndex = pendingVisibleTextIndex {
        let existing = parsed.visibleTextGroups[groupIndex]
        parsed.visibleTextGroups[groupIndex] = OpenedPhotoVisibleTextGroup(
          id: existing.id,
          text: existing.text,
          englishRendering: text
        )
        pendingVisibleTextIndex = nil
        continue
      }
      pendingVisibleTextIndex = nil

      switch name {
      case "Summary":
        parsed.summary = text
      case "Title":
        parsed.title = text
      case "Description":
        parsed.description = text
      case "Caption":
        parsed.caption = text
      case "Place shown":
        parsed.placeShown = text
      case "Captured":
        parsed.capturedRaw = text
      case "Capture zone":
        parsed.captureZone = text
      case "Where camera stood (Apple)":
        if text.caseInsensitiveCompare(placeNotRecordedText) != .orderedSame {
          parsed.cameraPlaceRaw = text
        }
      case "Camera GPS (standard coordinates)":
        parsed.coordinates = text
      case "Camera altitude (Apple)":
        parsed.altitude = text
      case "GPS horizontal accuracy (Apple)":
        parsed.gpsAccuracy = text
      case "Water (Apple)":
        parsed.water = text
      case "Island (Apple)":
        parsed.island = text
      case "Camera":
        parsed.camera = text
      case "Lens":
        parsed.lens = text
      case "Settings":
        parsed.settings = text
      case "People (Apple)":
        parsed.people = text
      case "Albums":
        parsed.albums = text
      case "Favourite":
        parsed.favourite = text
      case "Library":
        parsed.library = text
      case "Shared Library contributor (Apple)":
        parsed.contributor = text
      case "Editing state (Apple)":
        parsed.editingState = text
      case "Moment (Apple)":
        parsed.moment = text
      case "Dimensions":
        parsed.dimensions = text
      case "Duration":
        parsed.duration = text
      case "Trip (Apple)":
        parsed.trips.append(cleanTripText(text))
      case "Recognised text (Apple)":
        parsed.recognisedText = text
      case "Text":
        let group = OpenedPhotoVisibleTextGroup(
          id: "visible_text_\(index)",
          text: text,
          englishRendering: nil
        )
        parsed.visibleTextGroups.append(group)
        pendingVisibleTextIndex = parsed.visibleTextGroups.count - 1
      case "Card note":
        parsed.cardNote = text
      case "Picture":
        parsed.pictureFieldText = text
      default:
        let cleanedLabel = cleanFieldLabel(name)
        parsed.extraTechnicalFields.append(
          OpenedPhotoDetailField(
            id: "extra_\(index)_\(cleanedLabel)",
            label: cleanedLabel,
            value: text
          )
        )
      }
    }

    if parsed.description == nil, case .text(let bodyText) = body {
      let trimmedBody = bodyText.trimmingCharacters(in: .whitespacesAndNewlines)
      if !trimmedBody.isEmpty {
        parsed.description = trimmedBody
      }
    }
    if parsed.summary == nil, let subtitle {
      let trimmedSubtitle = subtitle.trimmingCharacters(in: .whitespacesAndNewlines)
      if !trimmedSubtitle.isEmpty {
        parsed.summary = trimmedSubtitle
      }
    }

    return parsed
  }

  private static func buildKeyFacts(
    formattedDate: String?,
    parsed: ParsedFields
  ) -> [OpenedPhotoDetailField] {
    var facts: [OpenedPhotoDetailField] = []

    if let formattedDate {
      facts.append(OpenedPhotoDetailField(id: "date", label: "Date", value: formattedDate))
    }

    let formattedCameraPlace = parsed.cameraPlaceRaw.map(formatPlaceForInspector)
    if let formattedCameraPlace {
      facts.append(
        OpenedPhotoDetailField(id: "location", label: "Location", value: formattedCameraPlace)
      )
    }

    if let placeShown = parsed.placeShown,
      formattedCameraPlace?.caseInsensitiveCompare(placeShown) != .orderedSame
    {
      facts.append(
        OpenedPhotoDetailField(id: "place_shown", label: "Place shown", value: placeShown)
      )
    }

    if !parsed.trips.isEmpty {
      let label = parsed.trips.count == 1 ? "Trip" : "Trips"
      facts.append(
        OpenedPhotoDetailField(
          id: "trip",
          label: label,
          value: parsed.trips.joined(separator: "\n")
        )
      )
    }

    if let people = parsed.people {
      facts.append(OpenedPhotoDetailField(id: "people", label: "People", value: people))
    }

    if let albums = parsed.albums {
      facts.append(OpenedPhotoDetailField(id: "albums", label: "Albums", value: albums))
    }

    let hasCameraHardware = parsed.camera != nil || parsed.settings != nil
    if hasCameraHardware {
      if let cameraSummary = formatCompactCameraSummary(
        camera: parsed.camera,
        settings: parsed.settings,
        dimensions: parsed.dimensions,
        duration: parsed.duration
      ) {
        facts.append(
          OpenedPhotoDetailField(id: "camera", label: "Camera", value: cameraSummary)
        )
      }
    } else {
      if let compactDimensions = compactPixelDimensions(parsed.dimensions) {
        facts.append(
          OpenedPhotoDetailField(id: "dimensions", label: "Dimensions", value: compactDimensions)
        )
      }
      if let duration = parsed.duration {
        facts.append(
          OpenedPhotoDetailField(id: "duration", label: "Duration", value: duration)
        )
      }
    }

    return facts
  }

  private static func buildTechnicalDetails(
    mediaKind: String,
    parsed: ParsedFields
  ) -> [OpenedPhotoDetailField] {
    var details: [OpenedPhotoDetailField] = [
      OpenedPhotoDetailField(id: "kind", label: "Kind", value: mediaKind)
    ]

    if let lens = parsed.lens {
      details.append(OpenedPhotoDetailField(id: "lens", label: "Lens", value: lens))
    }
    if let captureZone = parsed.captureZone {
      details.append(
        OpenedPhotoDetailField(id: "capture_zone", label: "Time zone", value: captureZone)
      )
    }
    if let coordinates = parsed.coordinates {
      details.append(
        OpenedPhotoDetailField(id: "coordinates", label: "Coordinates", value: coordinates)
      )
    }
    if let altitude = parsed.altitude {
      details.append(
        OpenedPhotoDetailField(id: "altitude", label: "Altitude", value: altitude)
      )
    }
    if let gpsAccuracy = parsed.gpsAccuracy {
      details.append(
        OpenedPhotoDetailField(id: "gps_accuracy", label: "GPS accuracy", value: gpsAccuracy)
      )
    }
    if let water = parsed.water,
      parsed.cameraPlaceRaw?.localizedCaseInsensitiveContains(water) != true
    {
      details.append(OpenedPhotoDetailField(id: "water", label: "Water", value: water))
    }
    if let island = parsed.island,
      parsed.cameraPlaceRaw?.localizedCaseInsensitiveContains(island) != true
    {
      details.append(OpenedPhotoDetailField(id: "island", label: "Island", value: island))
    }
    if let moment = parsed.moment {
      details.append(OpenedPhotoDetailField(id: "moment", label: "Moment", value: moment))
    }
    if let library = parsed.library {
      details.append(OpenedPhotoDetailField(id: "library", label: "Library", value: library))
    }
    if let contributor = parsed.contributor {
      details.append(
        OpenedPhotoDetailField(id: "contributor", label: "Contributor", value: contributor)
      )
    }
    if let editingState = parsed.editingState {
      details.append(
        OpenedPhotoDetailField(id: "editing", label: "Editing", value: editingState)
      )
    }
    if let favourite = parsed.favourite {
      details.append(
        OpenedPhotoDetailField(id: "favourite", label: "Favourite", value: favourite)
      )
    }
    if let cardNote = parsed.cardNote {
      details.append(
        OpenedPhotoDetailField(id: "card_note", label: "Card note", value: cardNote)
      )
    }

    details.append(contentsOf: parsed.extraTechnicalFields)
    return details
  }

  private static func trimmedTextValue(
    of field: TrawlerSpecificCommandDetailPresentationField
  ) -> String? {
    let raw: String
    switch field.fieldValue {
    case .text(let value):
      raw = value
    case .unsignedCount(let count):
      raw = count.formatted()
    case .archiveRecordAssociatedTime(let associatedTime):
      switch associatedTime {
      case .exactTime(let date):
        raw = date.formatted(date: .abbreviated, time: .shortened)
      case .calendarDate(let date):
        raw = date.formatted(date: .abbreviated, time: .omitted)
      }
    case .globallyRoutableTrawlLink(let link):
      raw = link.globallyRoutableTrawlLink
    case .exactMonetaryAmount(let amount):
      raw = amount.formattedDisplayString
    }
    let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
  }

  private static func concisePlaceName(placeShown: String?, cameraPlaceRaw: String?) -> String? {
    if let placeShown = placeShown?.trimmingCharacters(in: .whitespacesAndNewlines),
      !placeShown.isEmpty,
      placeShown.count <= 42
    {
      return placeShown
    }
    guard let cameraPlaceRaw else { return nil }
    let segments = cameraPlaceRaw
      .components(separatedBy: placeHierarchySeparator)
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty }
    return segments.first
  }

  private static func formatPlaceForInspector(_ rawPlace: String) -> String {
    let segments = rawPlace
      .components(separatedBy: placeHierarchySeparator)
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty }
    return segments.isEmpty ? rawPlace : segments.joined(separator: ", ")
  }

  private static func cleanTripText(_ rawTrip: String) -> String {
    rawTrip
      .replacingOccurrences(of: " (UTC dates)", with: "")
      .trimmingCharacters(in: .whitespacesAndNewlines)
  }

  private static func cleanCameraName(_ rawCamera: String?) -> String? {
    guard let trimmed = rawCamera?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty
    else {
      return nil
    }
    if trimmed.hasPrefix("Apple ") {
      let remainder = String(trimmed.dropFirst("Apple ".count))
      if remainder.hasPrefix("iPhone")
        || remainder.hasPrefix("iPad")
        || remainder.hasPrefix("iPod")
        || remainder.hasPrefix("Mac")
        || remainder.hasPrefix("Vision")
      {
        return remainder
      }
    }
    return trimmed
  }

  private static func compactCameraSettings(_ rawSettings: String?) -> String? {
    guard let trimmed = rawSettings?.trimmingCharacters(in: .whitespacesAndNewlines),
      !trimmed.isEmpty
    else {
      return nil
    }
    let parts = trimmed
      .components(separatedBy: " · ")
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty }

    var actualFocalLength: String?
    var equivalentFocalLength: String?
    var aperture: String?
    var otherParts: [String] = []

    for part in parts {
      if part.hasSuffix(" mm equivalent") {
        equivalentFocalLength = String(part.dropLast(" equivalent".count))
      } else if part.hasSuffix(" mm") {
        actualFocalLength = part
      } else if part.hasPrefix("f/") {
        aperture = part
      } else {
        otherParts.append(part)
      }
    }

    let chosenFocalLength = equivalentFocalLength ?? actualFocalLength
    var compactParts: [String] = []
    switch (chosenFocalLength, aperture) {
    case (let focal?, let fStop?):
      compactParts.append("\(focal) \(fStop)")
    case (let focal?, nil):
      compactParts.append(focal)
    case (nil, let fStop?):
      compactParts.append(fStop)
    case (nil, nil):
      break
    }
    compactParts.append(contentsOf: otherParts)
    return compactParts.isEmpty ? nil : compactParts.joined(separator: " · ")
  }

  private static func compactPixelDimensions(_ rawDimensions: String?) -> String? {
    guard let trimmed = rawDimensions?.trimmingCharacters(in: .whitespacesAndNewlines),
      !trimmed.isEmpty
    else {
      return nil
    }
    if trimmed.hasSuffix(" pixels") {
      return String(trimmed.dropLast(" pixels".count))
    }
    return trimmed
  }
}
