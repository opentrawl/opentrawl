import AppKit
import SwiftUI

/// Opens Full Disk Access, docks a draggable app tile inside the bottom of
/// System Settings, and watches a caller-supplied verified grant check.
@MainActor
public enum FullDiskAccessGuide {
  public static func present(
    grantCheck: @escaping @MainActor () -> Bool,
    completion: @escaping @MainActor (Bool) -> Void = { _ in }
  ) {
    active?.dismiss(granted: false)
    let controller = GuideController(grantCheck: grantCheck, completion: completion)
    active = controller
    controller.start()
  }

  public static func dismiss() {
    active?.dismiss(granted: false)
  }

  private static var active: GuideController?

  static func clear(_ controller: GuideController) {
    if active === controller {
      active = nil
    }
  }
}

@MainActor
final class GuideController {
  private static let settingsWindowFollowInterval: TimeInterval = 0.12

  private let grantCheck: @MainActor () -> Bool
  private let completion: @MainActor (Bool) -> Void
  private let poller = GrantPoller()
  private var panel: FloatingPanel?
  private var flight: ApplicationTileFlight?
  private var tileImage: NSImage?
  private var followTimer: Timer?
  private var sawSettingsWindow = false
  private var isFading = false
  private var finished = false

  init(
    grantCheck: @escaping @MainActor () -> Bool,
    completion: @escaping @MainActor (Bool) -> Void
  ) {
    self.grantCheck = grantCheck
    self.completion = completion
  }

  func start() {
    PermissionGuideController.openSystemSettings()

    let icon = appIcon()
    let name = applicationNameInSystemSettings()
    let model = GuideModel(icon: icon, appName: name, dragURL: Bundle.main.bundleURL)
    model.onClose = { [weak self] in self?.dismiss(granted: false) }

    let tileImage = ApplicationRowTile.renderedImage(icon: icon, appName: name)
    self.tileImage = tileImage
    if let takeOffFrame = ApplicationTileFlight.takeOffFrame() {
      flight = ApplicationTileFlight(
        tileImage: tileImage,
        takeOffFrame: takeOffFrame,
        landingSize: CGSize(width: ApplicationRowTile.width, height: 44)
      )
    }
    let panel = FloatingPanel(model: model, applicationTileDragImage: tileImage)
    panel.cancelHandler = { [weak self] in self?.dismiss(granted: false) }
    panel.alphaValue = 0
    self.panel = panel

    let timer = Timer(timeInterval: Self.settingsWindowFollowInterval, repeats: true) {
      [weak self] _ in
      MainActor.assumeIsolated { self?.followSettingsWindow() }
    }
    RunLoop.main.add(timer, forMode: .common)
    followTimer = timer
    followSettingsWindow()

    poller.start(check: grantCheck) { [weak self] in self?.grantLanded() }
  }

  /// Keeps the guide docked to System Settings as it opens, moves or
  /// resizes, and closes the guide when System Settings closes.
  private func followSettingsWindow() {
    guard let panel else { return }
    guard let settingsWindows = SettingsWindowLocator.current() else {
      if sawSettingsWindow && panel.model.phase == .guiding {
        dismiss(granted: false)
      }
      return
    }
    let settings = settingsWindows.mainWindowBounds
    let primaryScreen =
      (NSScreen.screens.first ?? NSScreen.main)?.frame
      ?? CGRect(x: 0, y: 0, width: 1440, height: 900)
    panel.setFrameOrigin(
      OverlayPlacement.dockedPanelOrigin(
        settings: settings,
        panelSize: panel.frame.size,
        transparentMargin: FloatingPanel.transparentMargin,
        primaryScreen: primaryScreen,
        visibleFrames: NSScreen.screens.map(\.visibleFrame)
      )
    )
    if !sawSettingsWindow {
      sawSettingsWindow = true
      panel.orderFrontRegardless()
      landFlight(in: panel)
    }
    // The guide floats above every System Settings window, so it steps
    // aside while the password prompt is up rather than cover its buttons.
    let visible = !settingsWindows.passwordPromptIsShowing || panel.model.phase == .granted
    panel.ignoresMouseEvents = !visible
    let targetAlpha: CGFloat = visible ? 1 : 0
    if panel.alphaValue != targetAlpha && !isFading {
      isFading = true
      NSAnimationContext.runAnimationGroup { context in
        context.duration = 0.2
        panel.animator().alphaValue = targetAlpha
      } completionHandler: { [weak self] in
        MainActor.assumeIsolated { self?.isFading = false }
      }
    }
  }

  /// Flies the page's tile into the docked tile's place, then reveals it.
  private func landFlight(in panel: FloatingPanel) {
    guard let flight else { return }
    self.flight = nil
    panel.contentView?.layoutSubtreeIfNeeded()
    guard let tileView = panel.model.dockedTileView, let window = tileView.window else {
      flight.cancel()
      return
    }
    let landingFrame = window.convertToScreen(tileView.convert(tileView.bounds, to: nil))
    panel.model.dockedTileIsWaitingForFlight = true
    let flew = flight.fly(to: landingFrame) { [weak panel] in
      panel?.model.dockedTileIsWaitingForFlight = false
    }
    if !flew {
      panel.model.dockedTileIsWaitingForFlight = false
    }
  }

  private func grantLanded() {
    guard !finished, let panel else { return }
    panel.model.phase = .granted
    Task { @MainActor in
      try? await Task.sleep(for: .milliseconds(1100))
      self.dismiss(granted: true)
    }
  }

  func dismiss(granted: Bool) {
    guard !finished else { return }
    finished = true
    flight?.cancel()
    flight = nil
    poller.stop()
    followTimer?.invalidate()
    followTimer = nil
    let outcome = granted || panel?.model.phase == .granted
    panel?.orderOut(nil)
    panel = nil
    completion(outcome)
    FullDiskAccessGuide.clear(self)
  }
}

/// Non-activating, and ordered front without becoming key, so System
/// Settings stays the active window. Esc works once the guide is clicked.
@MainActor
final class FloatingPanel: NSPanel {
  static let transparentMargin: CGFloat = 30

  let model: GuideModel
  var cancelHandler: () -> Void = {}

  init(model: GuideModel, applicationTileDragImage: NSImage) {
    self.model = model
    let hosting = NSHostingView(
      rootView: GuideOverlayView(
        model: model,
        applicationTileDragImage: applicationTileDragImage
      )
      .padding(Self.transparentMargin)
    )
    hosting.layout()
    super.init(
      contentRect: NSRect(origin: .zero, size: hosting.fittingSize),
      styleMask: [.borderless, .nonactivatingPanel],
      backing: .buffered,
      defer: false
    )
    contentView = hosting
    isOpaque = false
    backgroundColor = .clear
    hasShadow = false
    level = .floating
    hidesOnDeactivate = false
    collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
    animationBehavior = .none
  }

  override var canBecomeKey: Bool { true }
  override var canBecomeMain: Bool { false }
  override func cancelOperation(_ sender: Any?) { cancelHandler() }
}

/// The name System Settings lists this app under.
public func applicationNameInSystemSettings() -> String {
  (Bundle.main.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String)
    ?? (Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String)
    ?? ProcessInfo.processInfo.processName
}

@MainActor
private func appIcon() -> NSImage {
  NSApp?.applicationIconImage
    ?? NSWorkspace.shared.icon(forFile: Bundle.main.bundlePath)
}
