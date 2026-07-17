import AppKit
import CoreGraphics
import Foundation
import SwiftUI

@MainActor
public final class PermissionGuideController: NSObject, NSWindowDelegate {
  public static let settingsURL = URL(
    string:
      "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles"
  )!

  private let probe: FullDiskAccessProbe
  private var panel: NSPanel?
  private var pollingTask: Task<Void, Never>?
  private var onContinue: (() -> Void)?

  public init(probe: FullDiskAccessProbe = FullDiskAccessProbe()) {
    self.probe = probe
  }

  public func present(
    bundleURL: URL = Bundle.main.bundleURL,
    onContinue: @escaping () -> Void
  ) {
    present(bundleURL: bundleURL, copy: .legacyDefault, onContinue: onContinue)
  }

  public func present(
    bundleURL: URL = Bundle.main.bundleURL,
    copy: PermissionGuideCopy,
    onContinue: @escaping () -> Void
  ) {
    self.onContinue = onContinue
    NSWorkspace.shared.open(Self.settingsURL)

    if panel == nil {
      let panel = NSPanel(
        contentRect: CGRect(origin: .zero, size: CGSize(width: 280, height: 280)),
        styleMask: [.titled, .fullSizeContentView, .utilityWindow],
        backing: .buffered,
        defer: false
      )
      panel.title = copy.title
      panel.level = .floating
      panel.hidesOnDeactivate = false
      panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
      let icon = NSWorkspace.shared.icon(forFile: bundleURL.path)
      panel.contentView = NSHostingView(
        rootView: PermissionGuideView(
          bundleURL: bundleURL,
          icon: icon,
          copy: copy,
          onContinue: { [weak self] in self?.finishPermissionStep() }
        )
      )
      panel.delegate = self
      self.panel = panel
    }

    panel?.orderFrontRegardless()
    startPositioning()
    startPolling()
  }

  public func dismiss() {
    pollingTask?.cancel()
    pollingTask = nil
    panel?.close()
    panel = nil
    onContinue = nil
  }

  public func windowWillClose(_ notification: Notification) {
    pollingTask?.cancel()
    pollingTask = nil
    panel = nil
    onContinue = nil
  }

  private func startPositioning() {
    Task { @MainActor [weak self] in
      for delay in [Duration.milliseconds(350), .milliseconds(650), .seconds(1)] {
        try? await Task.sleep(for: delay)
        guard let self, self.panel != nil else { return }
        self.positionPanel()
      }
    }
  }

  private func startPolling() {
    pollingTask?.cancel()
    pollingTask = Task { @MainActor [weak self] in
      while !Task.isCancelled {
        guard let self else { return }
        if self.probe.status() == .granted {
          self.finishPermissionStep()
          return
        }
        try? await Task.sleep(for: .seconds(1))
      }
    }
  }

  private func finishPermissionStep() {
    let callback = onContinue
    dismiss()
    callback?()
  }

  private func positionPanel() {
    guard let panel,
      let quartzWindow = settingsWindowBounds(),
      let converted = GuidePlacement.convertFromQuartz(quartzWindow, displays: displays())
    else {
      panel?.center()
      return
    }
    panel.setFrame(
      GuidePlacement.panelFrame(
        beside: converted.frame,
        on: converted.display,
        panelSize: panel.frame.size
      ),
      display: true
    )
  }

  private func settingsWindowBounds() -> CGRect? {
    guard
      let app = NSRunningApplication.runningApplications(
        withBundleIdentifier: "com.apple.systempreferences"
      ).first,
      let windows = CGWindowListCopyWindowInfo(
        [.optionOnScreenOnly, .excludeDesktopElements],
        kCGNullWindowID
      ) as? [[CFString: Any]]
    else {
      return nil
    }

    return windows.compactMap { window -> CGRect? in
      guard (window[kCGWindowOwnerPID] as? NSNumber)?.int32Value == app.processIdentifier,
        (window[kCGWindowLayer] as? NSNumber)?.intValue == 0,
        let bounds = window[kCGWindowBounds] as? NSDictionary
      else {
        return nil
      }
      var rect = CGRect.zero
      return CGRectMakeWithDictionaryRepresentation(bounds, &rect) ? rect : nil
    }.max(by: { $0.width * $0.height < $1.width * $1.height })
  }

  private func displays() -> [DisplayGeometry] {
    NSScreen.screens.compactMap { screen in
      guard
        let number = screen.deviceDescription[
          NSDeviceDescriptionKey("NSScreenNumber")
        ] as? NSNumber
      else {
        return nil
      }
      let displayID = CGDirectDisplayID(number.uint32Value)
      return DisplayGeometry(
        quartzFrame: CGDisplayBounds(displayID),
        appKitFrame: screen.frame,
        visibleFrame: screen.visibleFrame
      )
    }
  }
}
