import AppKit
import SwiftUI

/// Opens Full Disk Access, floats a draggable app icon beside it, and watches
/// a caller-supplied verified grant check.
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

  private static var active: GuideController?

  static func clear(_ controller: GuideController) {
    if active === controller {
      active = nil
    }
  }
}

@MainActor
final class GuideController {
  private let grantCheck: @MainActor () -> Bool
  private let completion: @MainActor (Bool) -> Void
  private let poller = GrantPoller()
  private let placementRetrier = SettingsPlacementRetrier()
  private var panel: FloatingPanel?
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

    let model = GuideModel(
      icon: appIcon(),
      appName: appName(),
      dragURL: Bundle.main.bundleURL,
      pointerAngle: nil
    )
    model.onClose = { [weak self] in self?.dismiss(granted: false) }

    let panel = FloatingPanel(model: model)
    panel.cancelHandler = { [weak self] in self?.dismiss(granted: false) }
    self.panel = panel
    place(panel: panel, model: model, settingsBounds: nil)
    panel.makeKeyAndOrderFront(nil)

    placementRetrier.start(
      locate: SettingsWindowLocator.currentBounds,
      position: { [weak self, weak panel, weak model] bounds in
        guard let self, let panel, let model else { return }
        self.place(panel: panel, model: model, settingsBounds: bounds)
      }
    )

    poller.start(check: grantCheck) { [weak self] in self?.grantLanded() }
  }

  private func grantLanded() {
    guard !finished, let panel else { return }
    panel.model.phase = .granted
    Task { @MainActor in
      try? await Task.sleep(for: .milliseconds(950))
      self.dismiss(granted: true)
    }
  }

  func dismiss(granted: Bool) {
    guard !finished else { return }
    finished = true
    poller.stop()
    placementRetrier.stop()
    let outcome = granted || panel?.model.phase == .granted
    panel?.orderOut(nil)
    panel = nil
    completion(outcome)
    FullDiskAccessGuide.clear(self)
  }

  private func place(
    panel: FloatingPanel,
    model: GuideModel,
    settingsBounds: CGRect?
  ) {
    let primaryScreen =
      (NSScreen.screens.first ?? NSScreen.main)?.frame
      ?? CGRect(x: 0, y: 0, width: 1440, height: 900)
    let layout = OverlayPlacement.compute(
      settings: settingsBounds,
      panelSize: panel.frame.size,
      primaryScreen: primaryScreen,
      screens: NSScreen.screens.map(\.frame)
    )
    panel.setFrameOrigin(layout.origin)
    model.pointerAngle = layout.pointer
  }
}

/// Non-activating keeps System Settings usable; becoming key makes Esc work
/// from the first frame.
@MainActor
final class FloatingPanel: NSPanel {
  let model: GuideModel
  var cancelHandler: () -> Void = {}

  init(model: GuideModel) {
    self.model = model
    let hosting = NSHostingView(rootView: GuideOverlayView(model: model).padding(30))
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
    collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
    animationBehavior = .utilityWindow
  }

  override var canBecomeKey: Bool { true }
  override var canBecomeMain: Bool { false }
  override func cancelOperation(_ sender: Any?) { cancelHandler() }
}

private func appName() -> String {
  (Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String)
    ?? ProcessInfo.processInfo.processName
}

@MainActor
private func appIcon() -> NSImage {
  NSApp?.applicationIconImage
    ?? NSWorkspace.shared.icon(forFile: Bundle.main.bundlePath)
}
