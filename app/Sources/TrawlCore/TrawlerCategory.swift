import Foundation
import TrawlClient

public enum TrawlerFilterCategory: String, CaseIterable, Identifiable, Sendable, Equatable {
  case all
  case messages
  case email
  case notes
  case calendar
  case contacts
  case photos
  case money
  case health
  case social
  case plugins

  public var id: String { rawValue }

  public var displayName: String {
    switch self {
    case .all: "All"
    case .messages: "Messages"
    case .email: "Email"
    case .notes: "Notes"
    case .calendar: "Calendar"
    case .contacts: "Contacts"
    case .photos: "Photos"
    case .money: "Money"
    case .health: "Health"
    case .social: "Twitter"
    case .plugins: "Plugins"
    }
  }

  public var symbolName: String {
    switch self {
    case .all: "square.grid.2x2"
    case .messages: "bubble.left.and.bubble.right"
    case .email: "envelope"
    case .notes: "note.text"
    case .calendar: "calendar"
    case .contacts: "person.crop.circle"
    case .photos: "photo.on.rectangle"
    case .money: "banknote"
    case .health: "heart"
    case .social: "at"
    case .plugins: "puzzlepiece.extension"
    }
  }

  public static func category(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> TrawlerFilterCategory {
    switch registeredTrawler.registeredTrawlerIdentity.lowercased() {
    case "imessage", "whatsapp", "telegram", "beeper", "signal", "slack", "discord":
      .messages
    case "gmail", "mail":
      .email
    case "notes":
      .notes
    case "calendar":
      .calendar
    case "contacts":
      .contacts
    case "photos":
      .photos
    case "money":
      .money
    case "health":
      .health
    case "twitter", "x":
      .social
    default:
      .plugins
    }
  }

  public static func availableCategories(
    for trawlers: [RestingTrawler]
  ) -> [TrawlerFilterCategory] {
    let present = Set(
      trawlers
        .filter { $0.state != .comingSoon }
        .map { category(for: $0.id) }
    )
    return [.all] + allCases.filter { $0 != .all && present.contains($0) }
  }

  public func matches(_ registeredTrawler: RegisteredTrawlerIdentity) -> Bool {
    if self == .all { return true }
    return Self.category(for: registeredTrawler) == self
  }

  public func matchingTrawlers(in trawlers: [RestingTrawler]) -> [RestingTrawler] {
    if self == .all { return trawlers.filter { $0.state != .comingSoon } }
    return trawlers.filter { $0.state != .comingSoon && matches($0.id) }
  }
}

public enum BuiltInTrawlerRegistry {
  public static let builtInIdentities: Set<String> = [
    "imessage",
    "whatsapp",
    "telegram",
    "gmail",
    "mail",
    "notes",
    "contacts",
    "calendar",
    "photos",
    "money",
    "twitter",
    "health",
  ]

  public static func isExternalPlugin(_ registeredTrawler: RegisteredTrawlerIdentity) -> Bool {
    !builtInIdentities.contains(registeredTrawler.registeredTrawlerIdentity.lowercased())
  }
}
