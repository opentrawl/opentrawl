import Foundation
import TrawlClient

/// Explicit workspace destination so switching between home overview and search
/// never depends on whether a query or phase happens to be active.
public enum WorkspaceDestination: String, Sendable, Equatable, CaseIterable, Identifiable {
  case overview
  case search

  public var id: String { rawValue }

  public var title: String {
    switch self {
    case .overview: "Overview"
    case .search: "Search"
    }
  }
}

/// Stored user intent for the five local macOS apps that require Full Disk Access
/// (Messages, Mail, Notes, WhatsApp, Telegram). Kept separate from what macOS
/// currently reports so leaving those five apps out never looks like revoked access.
public enum FullDiskAccessChoice: String, Sendable, Equatable {
  case undecided
  case included
  case leftOut

  public static let defaultsKey = "OpenTrawlFullDiskAccessChoice"

  public static func load(from defaults: UserDefaults = .standard) -> Self {
    guard let raw = defaults.string(forKey: defaultsKey),
      let parsed = Self(rawValue: raw)
    else {
      return .undecided
    }
    return parsed
  }

  public func save(to defaults: UserDefaults = .standard) {
    defaults.set(rawValue, forKey: Self.defaultsKey)
  }
}

/// Single canonical per-app status model used across Overview, Search, Settings,
/// and Onboarding. Archived item counts stay separate from attention state so an
/// expired sign-in or closed app never hides how many items are already searchable.
public enum AppAttentionAction: Sendable, Equatable {
  case openFullDiskAccessSettings
  case openPrivacySettings(paneAnchor: String)
  case openSourceApp(bundleIdentifier: String, displayName: String)
  case signInGmail
  case signInX
  case retryUpdate
}

public struct AppOperabilityState: Sendable, Equatable {
  public let archivedItemCount: UInt64
  public let statusLine: String
  public let needsAttention: Bool
  public let actionTitle: String?
  public let action: AppAttentionAction?

  public init(
    archivedItemCount: UInt64,
    statusLine: String,
    needsAttention: Bool,
    actionTitle: String? = nil,
    action: AppAttentionAction? = nil
  ) {
    self.archivedItemCount = archivedItemCount
    self.statusLine = statusLine
    self.needsAttention = needsAttention
    self.actionTitle = actionTitle
    self.action = action
  }

  public static let fullDiskAccessTrawlerIDs: Set<String> = [
    "imessage",
    "mail",
    "notes",
    "whatsapp",
    "telegram",
  ]

  public static func resolve(
    trawler: RestingTrawler,
    status: TrawlerStatus?,
    updateProgress: TrawlerArchiveUpdateProgressState?,
    operationFailure: TrawlerOperationFailure?,
    isRemoteArchive: Bool,
    isInstalledOnThisMac: Bool,
    fullDiskAccessChoice: FullDiskAccessChoice
  ) -> Self {
    let contentCount =
      status?.archiveContentCountsAfterLastSuccessfullyCompletedUpdate.reduce(UInt64(0)) {
        $0 + $1.archiveContentCount
      } ?? 0
    let historyCount: UInt64 = {
      guard let history = status?.archiveWeeklyHistory else { return 0 }
      let dated = history.weeklyRecordCountsInChronologicalOrder.reduce(UInt64(0)) {
        $0 + $1.recordCount
      }
      return dated + history.undatedRecordCount
    }()
    let count = historyCount > 0 ? historyCount : contentCount
    let id = trawler.id.registeredTrawlerIdentity
    let name = trawler.registeredTrawlerDisplayName

    if let updateProgress {
      switch updateProgress {
      case .waiting:
        return Self(archivedItemCount: count, statusLine: "Waiting to update", needsAttention: false)
      case .building:
        return Self(archivedItemCount: count, statusLine: "Copying…", needsAttention: false)
      case .finalising:
        return Self(archivedItemCount: count, statusLine: "Finishing…", needsAttention: false)
      case .failed, .finished:
        break
      }
    }

    if isRemoteArchive {
      let line = count > 0 ? "\(count.formatted()) items" : "No items yet"
      return Self(archivedItemCount: count, statusLine: line, needsAttention: false)
    }

    if !isInstalledOnThisMac && count == 0 {
      if id == "telegram" {
        return Self(
          archivedItemCount: 0,
          statusLine: "Not installed (reads Telegram from the App Store, not Telegram Desktop).",
          needsAttention: false
        )
      }
      return Self(archivedItemCount: 0, statusLine: "Not installed", needsAttention: false)
    }

    if fullDiskAccessChoice == .leftOut && fullDiskAccessTrawlerIDs.contains(id) && count == 0 {
      return Self(
        archivedItemCount: 0,
        statusLine: "Left out. Needs Full Disk Access.",
        needsAttention: false,
        actionTitle: "Turn on…",
        action: .openFullDiskAccessSettings
      )
    }

    if let status {
      switch status.archiveLiveSyncCredentialStatus {
      case .notConnected:
        return signInState(id: id, count: count)
      case .requiresSignedApp:
        let line =
          count > 0
          ? "Earlier items are searchable. Sync needs a signed release build."
          : "Sync needs a signed release build."
        return Self(archivedItemCount: count, statusLine: line, needsAttention: false)
      case .available, .notApplicable:
        break
      }
    }

    if let failure = operationFailure {
      let lowerMessage = failure.failureMessage.lowercased()
      if failure.failureCode == .authentication
        || ((id == "twitter" || id == "x")
          && (lowerMessage.contains("no longer accepts")
            || lowerMessage.contains("sign in")
            || lowerMessage.contains("cookie")
            || lowerMessage.contains("auth")))
      {
        return signInState(id: id, count: count)
      }

      if failure.failureCode == .permission
        || lowerMessage.contains("operation not permitted")
        || lowerMessage.contains("permission")
        || lowerMessage.contains("snapshot sqlite")
        || lowerMessage.contains("store could not be read")
      {
        return permissionState(id: id, name: name, count: count)
      }

      if id == "telegram" && lowerMessage.contains("local data store is missing") {
        return Self(
          archivedItemCount: count,
          statusLine:
            count > 0
            ? "Open Telegram from the App Store to get new messages. Earlier items are searchable."
            : "Reads Telegram from the App Store, not Telegram Desktop.",
          needsAttention: count == 0,
          actionTitle: nil,
          action: nil
        )
      }

      let line =
        count > 0
        ? "Could not update just now. Earlier items are still searchable."
        : "Could not update just now."
      return Self(
        archivedItemCount: count,
        statusLine: line,
        needsAttention: true,
        actionTitle: "Try again",
        action: .retryUpdate
      )
    }

    if case .failed = updateProgress {
      let line =
        count > 0
        ? "Could not update just now. Earlier items are still searchable."
        : "Could not update just now."
      return Self(
        archivedItemCount: count,
        statusLine: line,
        needsAttention: true,
        actionTitle: "Try again",
        action: .retryUpdate
      )
    }

    if let status,
      status.sourceAppSyncLiveness == .sourceAppNotRunning,
      let bundleID = status.registeredTrawlerManifest.trawlerBranding?.bundleIdentifier,
      !bundleID.isEmpty
    {
      return Self(
        archivedItemCount: count,
        statusLine: "Open \(name) so OpenTrawl can get new messages.",
        needsAttention: true,
        actionTitle: "Open \(name)",
        action: .openSourceApp(bundleIdentifier: bundleID, displayName: name)
      )
    }

    let summary = count > 0 ? "\(count.formatted()) items" : "No items yet"
    return Self(archivedItemCount: count, statusLine: summary, needsAttention: false)
  }

  private static func permissionState(id: String, name: String, count: UInt64) -> Self {
    let suffix = count > 0 ? " Earlier items are still searchable." : ""
    if fullDiskAccessTrawlerIDs.contains(id) {
      return Self(
        archivedItemCount: count,
        statusLine: "Needs Full Disk Access.\(suffix)",
        needsAttention: true,
        actionTitle: "Open Settings",
        action: .openFullDiskAccessSettings
      )
    }
    let anchor: String? =
      switch id {
      case "photos": "Privacy_Photos"
      case "calendar": "Privacy_Calendars"
      case "contacts": "Privacy_Contacts"
      default: nil
      }
    if let anchor {
      return Self(
        archivedItemCount: count,
        statusLine: "Needs \(name) access in System Settings.\(suffix)",
        needsAttention: true,
        actionTitle: "Open Settings",
        action: .openPrivacySettings(paneAnchor: anchor)
      )
    }
    return Self(
      archivedItemCount: count,
      statusLine: "Needs permission in System Settings.\(suffix)",
      needsAttention: true,
      actionTitle: "Open Settings",
      action: .openFullDiskAccessSettings
    )
  }

  private static func signInState(id: String, count: UInt64) -> Self {
    if id == "gmail" {
      let line =
        count > 0
        ? "Signed out of Google. Earlier mail is still searchable."
        : "Sign in with Google in your browser. Can read mail, not send or change it."
      return Self(
        archivedItemCount: count,
        statusLine: line,
        needsAttention: true,
        actionTitle: count > 0 ? "Sign in again" : "Sign in…",
        action: .signInGmail
      )
    }
    if id == "twitter" || id == "x" {
      let line =
        count > 0
        ? "X signed you out. Earlier posts are still searchable."
        : "Sign in to X. OpenTrawl only reads your posts, likes, and older messages."
      return Self(
        archivedItemCount: count,
        statusLine: line,
        needsAttention: true,
        actionTitle: count > 0 ? "Sign in again" : "Sign in…",
        action: .signInX
      )
    }
    return Self(
      archivedItemCount: count,
      statusLine: "Sign in to update this app.",
      needsAttention: true
    )
  }
}

/// Time span zoom strictly for the Overview weekly chart. Changing zoom never
/// mutates the active Search tab date filter.
public enum OverviewChartSpan: String, Sendable, Equatable, CaseIterable, Identifiable {
  case oneYear = "1Y"
  case fiveYears = "5Y"
  case all = "All"

  public var id: String { rawValue }
}
