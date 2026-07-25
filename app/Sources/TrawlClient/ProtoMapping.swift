import Foundation

extension Trawl_App_V1_SyncSourceResult { fileprivate func model() throws -> SyncSourceResult { guard !appID.isEmpty else { throw TrawlClientError.invalidProtobuf }; return try SyncSourceResult(sourceID: appID, sourceName: surface, outcome: outcome.model(), failure: hasFailure ? failure.model() : nil) } }
extension Trawl_App_V1_SyncResponse { func model() throws -> SyncResponse { try SyncResponse(sources: sources.map { try $0.model() }, failures: failures.map { try $0.model() }, outcome: outcome.model()) } }

extension Trawl_App_V1_SyncProgress {
  func model() throws -> SyncProgress {
    guard !appID.isEmpty else { throw TrawlClientError.invalidProtobuf }
    switch phase {
    case .building: return SyncProgress.building(sourceID: appID)
    case .finalising: return SyncProgress.finalising(sourceID: appID)
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
