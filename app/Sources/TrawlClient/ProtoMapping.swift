import Foundation

extension Trawl_App_V1_OperationOutcome {
  fileprivate func model() throws -> OperationOutcome { switch self { case .complete: .complete; case .partial: .partial; case .failed: .failed; case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf } }
}
extension Trawl_App_V1_FailureCode { fileprivate func model() throws -> SourceFailureCode { switch self { case .unavailable: .unavailable; case .permission: .permission; case .authentication: .authentication; case .invalidInput: .invalidInput; case .notFound: .notFound; case .timeout: .timeout; case .internal: .internalError; case .alreadySyncing: .alreadySyncing; case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf } } }
extension Trawl_App_V1_SourceFailure { fileprivate func model() throws -> SourceFailure { try SourceFailure(sourceID: appID, sourceName: surface, code: code.model(), message: message, remedy: remedy) } }
extension Trawl_App_V1_SyncSourceResult { fileprivate func model() throws -> SyncSourceResult { guard !appID.isEmpty else { throw TrawlClientError.invalidProtobuf }; return try SyncSourceResult(sourceID: appID, sourceName: surface, outcome: outcome.model(), failure: hasFailure ? failure.model() : nil) } }
extension Trawl_App_V1_SyncResponse { func model() throws -> SyncResponse { try SyncResponse(sources: sources.map { try $0.model() }, failures: failures.map { try $0.model() }, outcome: outcome.model()) } }
