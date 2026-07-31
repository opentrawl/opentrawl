import Foundation

func isCanonicalTrawlerRecordReference(
  _ recordReference: CanonicalArchiveRecordReference,
  registeredTrawler: RegisteredTrawlerIdentity
) -> Bool {
  let canonicalArchiveRecordReference = recordReference.canonicalArchiveRecordReference
  let registeredTrawlerIdentity = registeredTrawler.registeredTrawlerIdentity
  return !registeredTrawlerIdentity.isEmpty
    && canonicalArchiveRecordReference
      == canonicalArchiveRecordReference.trimmingCharacters(in: .whitespacesAndNewlines)
    && canonicalArchiveRecordReference.hasPrefix("\(registeredTrawlerIdentity):")
    && canonicalArchiveRecordReference.dropFirst(registeredTrawlerIdentity.count + 1).contains {
      !$0.isWhitespace
    }
}

func isNonBlank(_ value: String) -> Bool {
  !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

func isValidAnchorIdentifier(_ anchor: RecordAnchorIdentifier) -> Bool {
  let value = anchor.recordAnchorIdentifier
  guard !value.isEmpty, value.utf8.count <= 128 else { return false }
  return value.utf8.allSatisfy {
    ($0 >= 65 && $0 <= 90) || ($0 >= 97 && $0 <= 122) || ($0 >= 48 && $0 <= 57)
      || $0 == 45 || $0 == 46 || $0 == 95
  }
}

func isValidSemanticKind(_ value: String) -> Bool {
  guard !value.isEmpty else { return false }
  return value.utf8.allSatisfy {
    ($0 >= 97 && $0 <= 122) || ($0 >= 48 && $0 <= 57) || $0 == 95
  }
}

extension Trawl_App_V1_SyncProgress {
  func decodedSyncProgress() throws -> SyncProgress {
    let syncingTrawler = syncingTrawler.decodedRegisteredTrawlerIdentity
    guard !syncingTrawler.registeredTrawlerIdentity.isEmpty else {
      throw TrawlClientError.invalidProtobuf
    }
    switch phase {
    case .building:
      return .building(syncingTrawler: syncingTrawler)
    case .finalising:
      return .finalising(syncingTrawler: syncingTrawler)
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}
