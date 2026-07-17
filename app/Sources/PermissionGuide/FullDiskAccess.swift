import Darwin
import Foundation

public enum FullDiskAccessStatus: Sendable, Equatable {
  case granted
  case denied
  case undetermined
}

public enum ProtectedPathOutcome: Sendable, Equatable {
  case readable
  case permissionDenied
  case missing
  case inconclusive
}

public struct FullDiskAccessProbe: Sendable {
  // macOS has no supported API for querying Full Disk Access. These canaries
  // report whether one known beta source can be opened, not the setting itself.
  public static let defaultCanaries = [
    URL(fileURLWithPath: "~/Library/Messages/chat.db".expandingTilde),
    URL(
      fileURLWithPath:
        "~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared/ChatStorage.sqlite"
        .expandingTilde
    ),
    URL(
      fileURLWithPath: "~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite"
        .expandingTilde
    ),
  ]

  private let canaries: [URL]
  private let probePath: @Sendable (URL) -> ProtectedPathOutcome

  public init(
    canaries: [URL] = FullDiskAccessProbe.defaultCanaries,
    probePath: @escaping @Sendable (URL) -> ProtectedPathOutcome = FullDiskAccessProbe.probe
  ) {
    self.canaries = canaries
    self.probePath = probePath
  }

  public func status() -> FullDiskAccessStatus {
    for canary in canaries {
      switch probePath(canary) {
      case .readable:
        return .granted
      case .permissionDenied:
        return .denied
      case .missing, .inconclusive:
        continue
      }
    }
    return .undetermined
  }

  public static func probe(_ url: URL) -> ProtectedPathOutcome {
    let descriptor = Darwin.open(url.path, O_RDONLY | O_CLOEXEC)
    guard descriptor >= 0 else {
      return classify(NSError(domain: NSPOSIXErrorDomain, code: Int(errno)))
    }
    _ = Darwin.close(descriptor)
    return .readable
  }

  public static func classify(_ error: Error) -> ProtectedPathOutcome {
    let error = error as NSError
    if error.domain == NSPOSIXErrorDomain {
      if error.code == Int(EACCES) || error.code == Int(EPERM) {
        return .permissionDenied
      }
      if error.code == Int(ENOENT) {
        return .missing
      }
    }
    if error.domain == NSCocoaErrorDomain {
      switch error.code {
      case NSFileReadNoPermissionError:
        return .permissionDenied
      case NSFileNoSuchFileError:
        return .missing
      default:
        break
      }
    }
    if let underlying = error.userInfo[NSUnderlyingErrorKey] as? Error {
      let classified = classify(underlying)
      if classified != .inconclusive {
        return classified
      }
    }
    return .inconclusive
  }
}

extension String {
  fileprivate var expandingTilde: String {
    (self as NSString).expandingTildeInPath
  }
}
