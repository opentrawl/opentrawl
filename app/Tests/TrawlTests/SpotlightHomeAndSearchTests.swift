import Foundation
import PermissionGuide
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct SpotlightHomeAndSearchTests {
  @Test func archiveCountsFormatRealNumbersAndSkipZeroCounts() {
    let counts = [
      ArchiveContentCountAfterLastSuccessfullyCompletedUpdate(
        archiveContentKindName: "messages",
        archiveContentKindDisplayName: "messages",
        archiveContentCount: 12_345
      ),
      ArchiveContentCountAfterLastSuccessfullyCompletedUpdate(
        archiveContentKindName: "conversations",
        archiveContentKindDisplayName: "conversations",
        archiveContentCount: 67
      ),
      ArchiveContentCountAfterLastSuccessfullyCompletedUpdate(
        archiveContentKindName: "empty",
        archiveContentKindDisplayName: "empty",
        archiveContentCount: 0
      ),
    ]

    let summary = TrawlerRestingCopy.formattedCountsSummary(from: counts)
    #expect(summary?.contains(UInt64(12_345).formatted()) == true)
    #expect(summary?.contains(UInt64(67).formatted()) == true)
    #expect(summary?.contains("empty") == false)
    #expect(TrawlerRestingCopy.formattedCountsSummary(from: []) == nil)
  }

  @Test func openedMessageTimelineOrdersOldestFirstAndNewestLast() {
    let newest = Date(timeIntervalSince1970: 1_700_003_600)
    let middle = Date(timeIntervalSince1970: 1_700_001_800)
    let oldest = Date(timeIntervalSince1970: 1_700_000_000)

    let wireNewestFirst = [
      MessageRecord(
        messageTime: .exactTime(newest),
        canonicalRecordReference: CanonicalArchiveRecordReference(canonicalArchiveRecordReference: "telegram:msg:3"),
        peopleRelatedToMessage: [],
        messageText: "latest",
        conversationDisplayName: "thread",
        messageMedia: nil
      ),
      MessageRecord(
        messageTime: .exactTime(middle),
        canonicalRecordReference: CanonicalArchiveRecordReference(canonicalArchiveRecordReference: "telegram:msg:2"),
        peopleRelatedToMessage: [],
        messageText: "matched",
        conversationDisplayName: "thread",
        messageMedia: nil
      ),
      MessageRecord(
        messageTime: .exactTime(oldest),
        canonicalRecordReference: CanonicalArchiveRecordReference(canonicalArchiveRecordReference: "telegram:msg:1"),
        peopleRelatedToMessage: [],
        messageText: "earliest",
        conversationDisplayName: "thread",
        messageMedia: nil
      ),
    ]

    let chronological = OpenedMessageTimeline.chronologicalMessages(from: wireNewestFirst)
    #expect(chronological.map(\.messageText) == ["earliest", "matched", "latest"])
  }

  @Test func searchRowFormatterStripsRedundantPrefixesAndDuplicateContainers() {
    let match = SearchMatch(
      trawlLink: GloballyRoutableTrawlLink(globallyRoutableTrawlLink: "telegram:abc1"),
      recordAnchor: RecordAnchorIdentifier(recordAnchorIdentifier: "msg"),
      searchMatchPresentation: SearchMatchPresentation(
        matchingRecordAssociatedTime: nil,
        registeredTrawlerDisplayName: "Telegram",
        matchingRecordDisplayName: "Channel",
        peopleRelatedToMatchingRecord: [
          PersonRelatedToArchiveRecord(
            personDisplayName: "SenderName",
            personRoleInArchiveRecord: .sender
          ),
          PersonRelatedToArchiveRecord(
            personDisplayName: "me",
            personRoleInArchiveRecord: .recipient
          ),
        ],
        digitalContainerNamesNearestToBroadest: ["Channel"],
        physicalPlaceNamesSpecificToBroadest: [],
        searchMatchTextFieldsInDisplayOrder: [],
        matchingRecordKindDisplayName: "message"
      )
    )

    let subtitle = SearchMatchRowFormatter.contextSubtitle(for: match, title: "Channel")
    #expect(subtitle == "SenderName")
    #expect(!SearchMatchRowFormatter.shouldShowFieldBadge(for: "message"))
    #expect(!SearchMatchRowFormatter.shouldShowFieldBadge(for: "body"))
    #expect(!SearchMatchRowFormatter.shouldShowFieldBadge(for: "title"))
    #expect(!SearchMatchRowFormatter.shouldShowFieldBadge(for: "description"))
    #expect(SearchMatchRowFormatter.shouldShowFieldBadge(for: "ocr"))
    #expect(SearchMatchRowFormatter.formattedFieldBadge(for: "ocr") == "OCR")
  }

  @Test func zeroFullDiskAccessAndMacFDAAppsAreDistinguishedClearly() {
    #expect(
      TrawlerAccessModel.resolve(
        for: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "health")
      ) == .zeroFullDiskAccess
    )
    #expect(
      TrawlerAccessModel.resolve(
        for: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "gmail")
      ) == .zeroFullDiskAccess
    )
    #expect(
      TrawlerAccessModel.resolve(
        for: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "telegram")
      ) == .zeroFullDiskAccess
    )
    #expect(
      TrawlerAccessModel.resolve(
        for: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "imessage")
      ) == .requiresMacFullDiskAccess
    )
    #expect(
      TrawlerAccessModel.resolve(
        for: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "notes")
      ) == .requiresMacFullDiskAccess
    )
    #expect(
      TrawlerFilterCategory.category(
        for: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "health")
      ) == .health
    )
    #expect(
      BuiltInTrawlerRegistry.isExternalPlugin(
        RegisteredTrawlerIdentity(registeredTrawlerIdentity: "beeper")
      )
    )
    #expect(
      !BuiltInTrawlerRegistry.isExternalPlugin(
        RegisteredTrawlerIdentity(registeredTrawlerIdentity: "health")
      )
    )
  }

  @Test func semanticStatusParserExtractsEnabledChunksAndModel() {
    let output = """
      Semantic search: enabled · 10,500 chunks · 42.1 MB · updated 2026-11-23 12:10 +01:00
      Model: Qwen3-Embedding-0.6B (512d) · Weights: cached (335.0 MB) · Helper: running
      """
    let snapshot = LocalSemanticSearchSnapshot.parse(from: output)
    #expect(snapshot.isAvailable)
    #expect(snapshot.isEnabled)
    #expect(snapshot.chunkSummary == "10,500 chunks")
    #expect(snapshot.sizeSummary == "42.1 MB")
    #expect(snapshot.modelSummary == "Qwen3-Embedding-0.6B (512d)")
    #expect(snapshot.helperSummary == "running")
  }

  @MainActor
  @Test func updateNowDoesNotBlockZeroFDAAppsWhenFullDiskAccessIsDenied() async {
    let client = RecordingTrawlClient()
    let deniedProbe = FullDiskAccessProbe(
      canaries: [URL(fileURLWithPath: "/nonexistent")],
      probePath: { _ in .permissionDenied }
    )
    let appModel = AppModel(client: client, permissionProbe: deniedProbe)

    await appModel.updateNow(
      registeredTrawlers: [RegisteredTrawlerIdentity(registeredTrawlerIdentity: "health")]
    )

    let recordedUpdates = await client.updatedTrawlers
    #expect(recordedUpdates == [[RegisteredTrawlerIdentity(registeredTrawlerIdentity: "health")]])
  }
}

private actor RecordingTrawlClient: TrawlClient {
  var updatedTrawlers: [[RegisteredTrawlerIdentity]] = []

  func status() async throws -> FederatedTrawlerStatusOperation {
    FederatedTrawlerStatusOperation(
      trawlerStatuses: [],
      operationFailures: [],
      trawlersSkippedFromOperation: [],
      outcome: .complete,
      registeredTrawlerCatalog: [],
      keepSourceAppsRunningForAutomaticUpdates: false
    )
  }

  func update(
    registeredTrawlers: [RegisteredTrawlerIdentity],
    progress _: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    updatedTrawlers.append(registeredTrawlers)
    return FederatedTrawlerArchiveUpdateOperation(
      trawlerArchiveUpdateResults: [],
      operationFailures: [],
      peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate: [],
      outcome: .complete
    )
  }

  func search(_: TrawlArchiveSearchRequest) async throws -> FederatedTrawlerSearchOperation {
    FederatedTrawlerSearchOperation(
      trawlerSearchResults: [],
      searchMatchesInDisplayOrder: [],
      operationFailures: [],
      trawlersSkippedFromOperation: [],
      outcome: .complete,
      resultLimit: 20,
      moreSearchMatchesExist: false
    )
  }

  func open(
    link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) async throws -> OpenResponse {
    OpenResponse(
      outcome: .failed,
      requestedTrawlLink: link,
      requestedRecordAnchor: anchor,
      record: nil,
      failure: nil
    )
  }
}
