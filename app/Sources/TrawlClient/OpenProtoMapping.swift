import Foundation

extension Trawl_Open_V1_OpenRecord {
  fileprivate func model() throws -> OpenRecord {
    guard !sourceID.isEmpty, !openRef.isEmpty, hasData, hasPresentation else { throw TrawlClientError.invalidProtobuf }
    return OpenRecord(sourceID: sourceID, openRef: openRef, typeURL: data.typeURL, value: data.value, presentation: try presentation.model())
  }
}
extension Trawl_Open_V1_OpenResponse {
  func model() throws -> OpenResponse {
    let outcome = try outcome.model()
    let record = hasRecord ? try self.record.model() : nil
    let failure = hasFailure ? try self.failure.model() : nil
    guard (outcome == .complete && record != nil && failure == nil) || (outcome == .failed && record == nil && failure != nil) else { throw TrawlClientError.invalidProtobuf }
    return OpenResponse(outcome: outcome, requestedRef: requestedRef, record: record, failure: failure)
  }
}
extension Trawl_Presentation_V1_Row.Role { fileprivate func model() throws -> PresentationRowRole { switch self { case .normal: .normal; case .target: .target; case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf } } }
extension Trawl_Presentation_V1_Resource.Kind { fileprivate func model() throws -> PresentationResourceKind { switch self { case .file: .file; case .image: .image; case .video: .video; case .audio: .audio; case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf } } }
extension Trawl_Presentation_V1_Fact.Kind { fileprivate func model() throws -> PresentationFactKind { switch self { case .truncation: .truncation; case .provenance: .provenance; case .warning: .warning; case .error: .error; case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf } } }
extension Trawl_Presentation_V1_Field { fileprivate func model() -> PresentationField { PresentationField(label: label, display: display) } }
extension Trawl_Presentation_V1_Row { fileprivate func model() throws -> PresentationRow { PresentationRow(role: try role.model(), cells: cells.map(\.display)) } }
extension Trawl_Presentation_V1_Resource { fileprivate func model() throws -> PresentationResource { PresentationResource(kind: try kind.model(), label: label, ref: ref, metadata: metadata.map { $0.model() }) } }
extension Trawl_Presentation_V1_Block {
  fileprivate func model() throws -> PresentationBlock {
    switch content {
    case let .heading(value): .heading(value.text)
    case let .prose(value): .prose(value.text)
    case let .fields(value): .fields(value.fields.map { $0.model() })
    case let .table(value): .table(columns: value.columns, rows: try value.rows.map { try $0.model() })
    case let .resource(value): .resource(try value.model())
    case .none: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Action {
  fileprivate func model() throws -> PresentationAction {
    switch target {
    case let .openRef(value) where !value.isEmpty: return PresentationAction(label: label, target: .openRef(value))
    case let .url(value): guard let url = URL(string: value), url.scheme?.lowercased() == "https" else { throw TrawlClientError.invalidProtobuf }; return PresentationAction(label: label, target: .url(url))
    default: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Presentation_V1_Fact { fileprivate func model() throws -> PresentationFact { PresentationFact(kind: try kind.model(), message: message, remedy: remedy) } }
extension Trawl_Presentation_V1_PresentationDocument { fileprivate func model() throws -> PresentationDocument { PresentationDocument(title: title, blocks: try blocks.map { try $0.model() }, actions: try actions.map { try $0.model() }, facts: try facts.map { try $0.model() }) } }
