import Foundation

extension Trawl_Open_V1_OpenRecord {
  fileprivate func model() throws -> OpenRecord {
    guard !sourceID.isEmpty, isCanonicalSourceRef(openRef, sourceID: sourceID), hasData,
      !data.typeURL.isEmpty, hasPresentation
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return OpenRecord(
      sourceID: sourceID, openRef: openRef, typeURL: data.typeURL, value: data.value,
      presentation: try presentation.model(sourceID: sourceID))
  }
}
extension Trawl_Open_V1_OpenResponse {
  func model() throws -> OpenResponse {
    let outcome = try outcome.model()
    let record = hasRecord ? try self.record.model() : nil
    let failure = hasFailure ? try self.failure.model() : nil
    guard
      (outcome == .complete && record != nil && failure == nil && !requestedAnchorID.isEmpty
        && record?.presentation.containsAnchor(requestedAnchorID) == true)
        || (outcome == .failed && record == nil && failure != nil)
    else { throw TrawlClientError.invalidProtobuf }
    return OpenResponse(
      outcome: outcome, requestedRef: requestedRef, requestedAnchorID: requestedAnchorID,
      record: record, failure: failure)
  }
}
extension Trawl_Presentation_V1_Row.Role {
  fileprivate func model() throws -> PresentationRowRole {
    switch self {
    case .normal: .normal
    case .target: .target
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Resource.Kind {
  fileprivate func model() throws -> PresentationResourceKind {
    switch self {
    case .file: .file
    case .image: .image
    case .video: .video
    case .audio: .audio
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Fact.Kind {
  fileprivate func model() throws -> PresentationFactKind {
    switch self {
    case .truncation: .truncation
    case .provenance: .provenance
    case .warning: .warning
    case .error: .error
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Field {
  fileprivate func model() throws -> PresentationField {
    guard isNonBlank(label), isNonBlank(display) else { throw TrawlClientError.invalidProtobuf }
    return PresentationField(label: label, display: display, anchorID: anchorID)
  }
}
extension Trawl_Presentation_V1_Row {
  fileprivate func model(columnCount: Int) throws -> PresentationRow {
    guard cells.count == columnCount else { throw TrawlClientError.invalidProtobuf }
    return PresentationRow(role: try role.model(), cells: cells.map(\.display), anchorID: anchorID)
  }
}
extension Trawl_Presentation_V1_Resource {
  fileprivate func model(sourceID: String) throws -> PresentationResource {
    guard isNonBlank(label), isCanonicalSourceRef(ref, sourceID: sourceID) else {
      throw TrawlClientError.invalidProtobuf
    }
    return PresentationResource(
      kind: try kind.model(), label: label, ref: ref,
      metadata: try metadata.map { try $0.model() },
      anchorID: anchorID)
  }
}
extension Trawl_Presentation_V1_ResourceResponse {
  func model() throws -> PresentationResourceData {
    let contentParts = contentType.split(separator: "/", omittingEmptySubsequences: false)
    guard !resourceRef.isEmpty, contentParts.count == 2,
      contentType.allSatisfy({ !$0.isWhitespace && !$0.isNewline }), !data.isEmpty,
      data.count <= Int(ProcessTrawlClient.maximumResourceBytes)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return PresentationResourceData(ref: resourceRef, contentType: contentType, data: data)
  }
}
extension Trawl_Presentation_V1_Block {
  fileprivate func model(sourceID: String) throws -> PresentationBlock {
    switch content {
    case .heading(let value) where isNonBlank(value.text):
      return .heading(anchorID: anchorID, text: value.text)
    case .prose(let value) where isNonBlank(value.text):
      return .prose(anchorID: anchorID, text: value.text)
    case .fields(let value) where !value.fields.isEmpty:
      return .fields(anchorID: anchorID, try value.fields.map { try $0.model() })
    case .table(let value):
      guard !value.columns.isEmpty, value.columns.allSatisfy(isNonBlank) else {
        throw TrawlClientError.invalidProtobuf
      }
      return .table(
        anchorID: anchorID, columns: value.columns,
        rows: try value.rows.map { try $0.model(columnCount: value.columns.count) })
    case .resource(let value):
      return .resource(anchorID: anchorID, try value.model(sourceID: sourceID))
    default: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Action {
  fileprivate func model(sourceID: String) throws -> PresentationAction {
    guard isNonBlank(label) else { throw TrawlClientError.invalidProtobuf }
    switch target {
    case .openRef(let value) where isCanonicalSourceRef(value, sourceID: sourceID):
      return PresentationAction(label: label, target: .openRef(value))
    case .url(let value):
      guard let url = URL(string: value), url.scheme?.lowercased() == "https" else {
        throw TrawlClientError.invalidProtobuf
      }
      return PresentationAction(label: label, target: .url(url))
    default: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Fact {
  fileprivate func model() throws -> PresentationFact {
    guard isNonBlank(message) else { throw TrawlClientError.invalidProtobuf }
    return PresentationFact(kind: try kind.model(), message: message, remedy: remedy)
  }
}
extension Trawl_Presentation_V1_PresentationDocument {
  fileprivate func model(sourceID: String) throws -> PresentationDocument {
    guard isNonBlank(title) else { throw TrawlClientError.invalidProtobuf }
    try validateAnchors()
    return PresentationDocument(
      title: title, primaryAnchorID: primaryAnchorID,
      blocks: try blocks.map { try $0.model(sourceID: sourceID) },
      actions: try actions.map { try $0.model(sourceID: sourceID) },
      facts: try facts.map { try $0.model() })
  }

  private func validateAnchors() throws {
    guard isValidAnchorID(primaryAnchorID) else { throw TrawlClientError.invalidProtobuf }
    var anchors = Set<String>()
    var primaryCount = 0
    func add(_ anchorID: String) throws {
      guard anchorID.isEmpty || isValidAnchorID(anchorID) && anchors.insert(anchorID).inserted
      else { throw TrawlClientError.invalidProtobuf }
      if anchorID == primaryAnchorID { primaryCount += 1 }
    }
    for block in blocks {
      try add(block.anchorID)
      switch block.content {
      case .fields(let value):
        for field in value.fields { try add(field.anchorID) }
      case .table(let value):
        for row in value.rows { try add(row.anchorID) }
      case .resource(let value):
        try add(value.anchorID)
        for field in value.metadata { try add(field.anchorID) }
      default:
        break
      }
    }
    guard primaryCount == 1 else { throw TrawlClientError.invalidProtobuf }
  }
}
