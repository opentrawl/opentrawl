import Foundation
import TrawlClient

enum OpenTrawlCommandDemoInstruction: Sendable, Equatable {
  case changeToPackagedHelperDirectory
  case runTrawl(arguments: [String])
  case searchNewestRecords
  case openNewestSearchResult

  func displayedCommand(helperDirectoryPath: String, arguments: [String]) -> String {
    switch self {
    case .changeToPackagedHelperDirectory:
      "cd \(helperDirectoryPath)"
    default:
      (["./trawl"] + arguments).joined(separator: " ")
    }
  }
}

struct OpenTrawlCommandDemoStep: Sendable, Equatable {
  let comment: String
  let instruction: OpenTrawlCommandDemoInstruction
}

enum OpenTrawlCommandDemoScript {
  static let steps: [OpenTrawlCommandDemoStep] = [
    step(
      DraftCopy.CommandDemo.changeDirectoryComment,
      .changeToPackagedHelperDirectory
    ),
    step(
      DraftCopy.CommandDemo.statusComment,
      .runTrawl(arguments: ["status"])
    ),
    step(DraftCopy.CommandDemo.searchComment, .searchNewestRecords),
    step(DraftCopy.CommandDemo.searchResultComment, .openNewestSearchResult),
    step(
      DraftCopy.CommandDemo.conversationsComment,
      .runTrawl(arguments: ["imessage", "conversations", "--limit", "10"])
    ),
    step(
      DraftCopy.CommandDemo.whatsAppComment,
      .runTrawl(arguments: ["whatsapp", "messages", "--limit", "5"])
    ),
    step(
      DraftCopy.CommandDemo.telegramComment,
      .runTrawl(arguments: ["telegram", "messages", "--limit", "10"])
    ),
    step(
      DraftCopy.CommandDemo.notesComment,
      .runTrawl(arguments: ["notes", "notes", "--limit", "10"])
    ),
    step(
      DraftCopy.CommandDemo.contactsComment,
      .runTrawl(arguments: ["contacts", "people", "--limit", "10"])
    ),
    step(
      DraftCopy.CommandDemo.calendarComment,
      .runTrawl(arguments: ["calendar", "events", "--limit", "10"])
    ),
  ]

  private static func step(
    _ comment: String,
    _ instruction: OpenTrawlCommandDemoInstruction
  ) -> OpenTrawlCommandDemoStep {
    OpenTrawlCommandDemoStep(
      comment: comment,
      instruction: instruction
    )
  }
}

actor OpenTrawlCommandDemoJourney {
  private let client: any TrawlClient
  private var newestSearchResultLink: GloballyRoutableTrawlLink?

  init(client: any TrawlClient) {
    self.client = client
  }

  func resolveCommandArguments(
    for instruction: OpenTrawlCommandDemoInstruction
  ) async -> [String]? {
    switch instruction {
    case .changeToPackagedHelperDirectory:
      return []
    case .runTrawl(let arguments):
      return arguments
    case .searchNewestRecords:
      let earliestArchiveRecordTime = Date(timeIntervalSince1970: 0)
      guard
        let response = try? await client.search(
          TrawlArchiveSearchRequest(
            earliestMatchingArchiveRecordTimeInclusive: earliestArchiveRecordTime,
            maximumReturnedSearchMatchCount: 5
          )
        ),
        let firstSearchResult = response.searchMatchesInDisplayOrder.first
      else {
        return nil
      }
      newestSearchResultLink = firstSearchResult.trawlLink
      return ["search", "--after", "1970-01-01", "--limit", "5"]
    case .openNewestSearchResult:
      guard let newestSearchResultLink else { return nil }
      return ["open", newestSearchResultLink.globallyRoutableTrawlLink]
    }
  }
}
