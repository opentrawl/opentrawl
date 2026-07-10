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
      URL(fileURLWithPath: "/synthetic/denied"),
      URL(fileURLWithPath: "/synthetic/readable"),
    ],
    probePath: { $0.lastPathComponent == "readable" ? .readable : .permissionDenied }
  )

  #expect(probe.status() == .granted)
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
