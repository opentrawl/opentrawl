import Foundation
import TrawlCore

enum OperationalCopy {
  enum SharedAction {
    static let cancel = "Stop"
    static let continueAction = "Continue"
    static let retry = "Try again"
  }

  enum FullDiskAccess {
    static let open = "Open Full Disk Access"
    static let idle = "Full Disk Access is not confirmed yet"
    static let checking = "Checking access…"
    static let confirmed = "Full Disk Access confirmed"
    static let notConfirmed = "OpenTrawl cannot confirm Full Disk Access yet."
    static let instruction = "In Full Disk Access, turn on OpenTrawl, then return to the app."
    static let needed = "Full Disk Access needed"
  }

  enum ArchiveBuild {
    static let copyAIInstructions = "Copy instructions for your AI"
    static let copiedAIInstructions = "Instructions copied"
  }

  enum CommandDemo {
    static let terminalTitle = "trawl"
    static let changeDirectoryComment = "Open the command-line tool folder"
    static let statusComment = "Check which apps are searchable"
    static let searchComment = "Search your archive for hello"
    static let searchResultComment = "Open the first search result"
    static let conversationsComment = "List recent Messages conversations"
    static let whatsAppComment = "List recent WhatsApp messages"
    static let telegramComment = "List recent Telegram messages"
    static let notesComment = "List recent notes"
    static let contactsComment = "List people in Contacts"
    static let calendarComment = "List upcoming events"
    static let helperUnavailableOutput =
      "The command-line tool is missing from this build. Use the app to search."
    static let commandFailedOutput =
      "This command did not run. Try again, or use the app to search."
    static let copyCommand = "Copy command"
    static let copiedCommand = "Command copied"
  }

  enum AppStatus {
    static let waiting = "Waiting"
    static let building = "Building…"
    static let finalising = "Finalising…"
    static let searchable = "Searchable"
    static let searchableWithFailedUpdate = "Search works · update failed"
    static let updateFailed = "Update failed"
    static let notSearchable = "Not searchable"
    static let notInstalled = "Not installed"
    static let notAvailable = "Not available"
    static let appsUnavailable = "Apps unavailable"
    static let statusCheckFailed = "OpenTrawl could not check your apps."
    static let statusCheckRecovery = "Try again. Apps that already work remain available."
  }

  enum Home {
    static let unavailableApp =
      "Not available. Other apps still work. Update your archive and try again."
  }

  enum Search {
    static func retainedResults(for phase: SearchPhase, query: String?) -> String? {
      let previousSearch = query ?? "the previous search"
      return switch phase {
      case .loading:
        "Showing results for \(previousSearch) while searching."
      case .timedOut:
        "Showing results for \(previousSearch). The new search timed out. Try again."
      case .failed:
        "Showing results for \(previousSearch). The new search failed. Try again."
      default:
        nil
      }
    }

    static let partialResults =
      "Some apps could not be searched. Results from other apps are shown."

    static func outcomeTitle(for phase: SearchPhase) -> String {
      return switch phase {
      case .complete, .partial:
        "No matches"
      case .skipped, .failed:
        "Search unavailable"
      case .timedOut:
        "Search timed out"
      case .idle, .loading:
        "Search"
      }
    }

    static func outcomeDetail(
      for phase: SearchPhase,
      isScoped: Bool,
      timedOutLocally: Bool,
      timeoutSeconds: Int
    ) -> String {
      return switch phase {
      case .complete:
        ""
      case .partial:
        isScoped
          ? "This app could not be searched. Your archive is unchanged. Try again."
          : "Some apps could not be searched. Try again, or search one app."
      case .skipped:
        isScoped
          ? "This app is not available for search. Try another app."
          : "Some apps are not available for search. Try another app."
      case .failed:
        "Search failed. Your archive is unchanged. Try again."
      case .timedOut:
        timedOutLocally
          ? "Search stopped after \(timeoutSeconds) seconds. Your archive is unchanged. Try again."
          : "Search timed out. Your archive is unchanged. Try again."
      case .idle, .loading:
        ""
      }
    }
  }

  enum Record {
    static let unavailableTitle = "Result unavailable"
    static let unavailableDetail =
      "OpenTrawl could not read this result. Choose another result or try again."
    static let timedOutDetail =
      "Opening this result took too long. Choose another result or try again."
    static let noteBodyUnavailable =
      "The note text is not available. Choose another result."
  }
}
