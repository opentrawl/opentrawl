import Foundation
import Testing

@testable import PermissionGuide

@Test func missingCanariesDoNotPretendAccessWasDenied() {
  let probe = FullDiskAccessProbe(
    canaries: [URL(fileURLWithPath: "/synthetic/missing")],
    probePath: { _ in .missing }
  )

  #expect(probe.status() == .undetermined)
}

@Test func explicitPermissionErrorMeansAccessWasDenied() {
  let probe = FullDiskAccessProbe(
    canaries: [URL(fileURLWithPath: "/synthetic/protected")],
    probePath: { _ in .permissionDenied }
  )

  #expect(probe.status() == .denied)
}

@Test func oneReadableProtectedPathProvesAccess() {
  let probe = FullDiskAccessProbe(
    canaries: [
      URL(fileURLWithPath: "/synthetic/missing"),
      URL(fileURLWithPath: "/synthetic/readable"),
    ],
    probePath: { $0.lastPathComponent == "readable" ? .readable : .missing }
  )

  #expect(probe.status() == .granted)
}

@Test func defaultCanariesBelongOnlyToBetaSources() {
  let paths = FullDiskAccessProbe.defaultCanaries.map(\.path)

  #expect(paths.count == 3)
  #expect(paths.contains { $0.hasSuffix("/Library/Messages/chat.db") })
  #expect(paths.contains { $0.hasSuffix("/ChatStorage.sqlite") })
  #expect(paths.contains { $0.hasSuffix("/group.com.apple.notes/NoteStore.sqlite") })
  #expect(!paths.contains { $0.contains("/Library/Mail") })
  #expect(!paths.contains { $0.contains("/Library/Safari") })
}

@Test func probeStopsAfterTheFirstDecisiveResult() {
  let recorder = PathRecorder()
  let probe = FullDiskAccessProbe(
    canaries: [
      URL(fileURLWithPath: "/synthetic/denied"),
      URL(fileURLWithPath: "/synthetic/untouched"),
    ],
    probePath: { url in
      recorder.record(url)
      return .permissionDenied
    }
  )

  #expect(probe.status() == .denied)
  #expect(recorder.paths.map(\.lastPathComponent) == ["denied"])
}

@Test func classifiesPosixPermissionAndMissingErrors() {
  #expect(
    FullDiskAccessProbe.classify(
      NSError(domain: NSPOSIXErrorDomain, code: Int(EACCES))
    ) == .permissionDenied
  )
  #expect(
    FullDiskAccessProbe.classify(
      NSError(domain: NSPOSIXErrorDomain, code: Int(ENOENT))
    ) == .missing
  )
}

private final class PathRecorder: @unchecked Sendable {
  private let lock = NSLock()
  private var recordedPaths: [URL] = []

  var paths: [URL] {
    lock.withLock { recordedPaths }
  }

  func record(_ url: URL) {
    lock.withLock { recordedPaths.append(url) }
  }
}
