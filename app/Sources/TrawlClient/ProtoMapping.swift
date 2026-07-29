import Foundation

func isCanonicalTrawlerRecordReference(
  _ recordReference: String,
  registeredTrawlerManifestIdentity: String
) -> Bool {
  !registeredTrawlerManifestIdentity.isEmpty
    && recordReference == recordReference.trimmingCharacters(in: .whitespacesAndNewlines)
    && recordReference.hasPrefix("\(registeredTrawlerManifestIdentity):")
    && recordReference.dropFirst(registeredTrawlerManifestIdentity.count + 1).contains {
      !$0.isWhitespace
    }
}

func isNonBlank(_ value: String) -> Bool {
  !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

func isValidAnchorIdentifier(_ value: String) -> Bool {
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
  func model() throws -> SyncProgress {
    guard !registeredTrawlerManifestIdentity.isEmpty else {
      throw TrawlClientError.invalidProtobuf
    }
    switch phase {
    case .building:
      return .building(
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
    case .finalising:
      return .finalising(
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}
