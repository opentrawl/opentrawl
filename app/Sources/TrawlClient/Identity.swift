public struct RegisteredTrawlerIdentity: Sendable, Equatable, Hashable {
  public let registeredTrawlerIdentity: String

  public init(registeredTrawlerIdentity: String) {
    self.registeredTrawlerIdentity = registeredTrawlerIdentity
  }
}

public struct CanonicalArchiveRecordReference: Sendable, Equatable, Hashable {
  public let canonicalArchiveRecordReference: String

  public init(canonicalArchiveRecordReference: String) {
    self.canonicalArchiveRecordReference = canonicalArchiveRecordReference
  }
}

public struct LocalTrawlerShortReference: Sendable, Equatable, Hashable {
  public let localTrawlerShortReference: String

  public init(localTrawlerShortReference: String) {
    self.localTrawlerShortReference = localTrawlerShortReference
  }
}

public struct GloballyRoutableTrawlLink: Sendable, Equatable, Hashable {
  public let globallyRoutableTrawlLink: String

  public init(globallyRoutableTrawlLink: String) {
    self.globallyRoutableTrawlLink = globallyRoutableTrawlLink
  }
}

public struct RecordAnchorIdentifier: Sendable, Equatable, Hashable {
  public let recordAnchorIdentifier: String

  public init(recordAnchorIdentifier: String) {
    self.recordAnchorIdentifier = recordAnchorIdentifier
  }
}

public struct ExactPersonFilterIdentifier: Sendable, Equatable, Hashable {
  public let exactPersonFilterIdentifier: String

  public init(exactPersonFilterIdentifier: String) {
    self.exactPersonFilterIdentifier = exactPersonFilterIdentifier
  }
}

extension Trawl_Identity_RegisteredTrawlerIdentity {
  var decodedRegisteredTrawlerIdentity: RegisteredTrawlerIdentity {
    RegisteredTrawlerIdentity(registeredTrawlerIdentity: registeredTrawlerIdentity)
  }
}

extension Trawl_Identity_CanonicalArchiveRecordReference {
  var decodedCanonicalArchiveRecordReference: CanonicalArchiveRecordReference {
    CanonicalArchiveRecordReference(
      canonicalArchiveRecordReference: canonicalArchiveRecordReference)
  }
}

extension Trawl_Identity_LocalTrawlerShortReference {
  var decodedLocalTrawlerShortReference: LocalTrawlerShortReference {
    LocalTrawlerShortReference(localTrawlerShortReference: localTrawlerShortReference)
  }
}

extension Trawl_Identity_GloballyRoutableTrawlLink {
  var decodedGloballyRoutableTrawlLink: GloballyRoutableTrawlLink {
    GloballyRoutableTrawlLink(globallyRoutableTrawlLink: globallyRoutableTrawlLink)
  }
}

extension Trawl_Identity_RecordAnchorIdentifier {
  var decodedRecordAnchorIdentifier: RecordAnchorIdentifier {
    RecordAnchorIdentifier(recordAnchorIdentifier: recordAnchorIdentifier)
  }
}

extension Trawl_Person_ExactPersonFilterIdentifier {
  var decodedExactPersonFilterIdentifier: ExactPersonFilterIdentifier {
    ExactPersonFilterIdentifier(exactPersonFilterIdentifier: exactPersonFilterIdentifier)
  }
}
