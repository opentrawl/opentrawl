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
  public static let defaultCanaries = [
    URL(fileURLWithPath: "~/Library/Messages/chat.db".expandingTilde),
    URL(fileURLWithPath: "~/Library/Mail".expandingTilde, isDirectory: true),
    URL(fileURLWithPath: "~/Library/Safari/History.db".expandingTilde),
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
    var foundDenial = false
    for canary in canaries {
      switch probePath(canary) {
      case .readable:
        return .granted
      case .permissionDenied:
        foundDenial = true
      case .missing, .inconclusive:
        continue
      }
    }
    return foundDenial ? .denied : .undetermined
  }

  public static func probe(_ url: URL) -> ProtectedPathOutcome {
    var isDirectory: ObjCBool = false
    if FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) {
      do {
        if isDirectory.boolValue {
          _ = try FileManager.default.contentsOfDirectory(
            at: url,
            includingPropertiesForKeys: nil,
            options: [.skipsHiddenFiles]
          )
        } else {
          let handle = try FileHandle(forReadingFrom: url)
          try handle.close()
        }
        return .readable
      } catch {
        return classify(error)
      }
    }

    do {
      _ = try FileManager.default.attributesOfItem(atPath: url.path)
      return .inconclusive
    } catch {
      return classify(error)
    }
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
