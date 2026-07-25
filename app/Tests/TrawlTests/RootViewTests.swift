import AppKit
import PermissionGuide
import SwiftUI
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct RootViewTests {
  @MainActor
  @Test func onboardingAndMainProductUseOneFixedWindowContract() {
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: TrawlDesign.defaultWindow),
      styleMask: [.titled, .closable, .miniaturizable, .resizable],
      backing: .buffered,
      defer: false
    )
    window.collectionBehavior = [.fullScreenPrimary]
    window.tabbingMode = .preferred
    let coordinator = WindowBehavior.Coordinator()
    coordinator.apply(isOnboarding: true, to: window)
    #expect(window.contentLayoutRect.size == TrawlDesign.onboardingWindow)
    #expect(!window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.onboardingWindow)
    #expect(window.contentMaxSize == TrawlDesign.onboardingWindow)
    #expect(window.collectionBehavior.contains(.fullScreenNone))
    #expect(!window.collectionBehavior.contains(.fullScreenPrimary))
    #expect(window.tabbingMode == .disallowed)
    #expect(window.standardWindowButton(.zoomButton)?.isHidden == true)
    #expect(window.titleVisibility == .hidden)
    #expect(window.titlebarAppearsTransparent)
    if let toolbarButton = window.standardWindowButton(.toolbarButton) {
      #expect(toolbarButton.isHidden)
    }
    #expect(window.level == .normal)

    coordinator.apply(isOnboarding: false, to: window)
    #expect(window.contentLayoutRect.size == TrawlDesign.defaultWindow)
    #expect(!window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.defaultWindow)
    #expect(window.contentMaxSize == TrawlDesign.defaultWindow)
    #expect(window.collectionBehavior.contains(.fullScreenNone))
    #expect(!window.collectionBehavior.contains(.fullScreenPrimary))
    #expect(window.tabbingMode == .disallowed)
    #expect(window.level == .normal)
    #expect(window.standardWindowButton(.zoomButton)?.isHidden == true)
    #expect(window.titleVisibility == .visible)
    #expect(!window.titlebarAppearsTransparent)
  }

  @MainActor
  @Test func onboardingScaffoldDoesNotCreateAHiddenScrollView() {
    let host = NSHostingView(
      rootView: TrawlFlowScaffold(page: .welcome) {
        Color.clear
      } actions: {
        EmptyView()
      }
      .frame(
        width: TrawlDesign.onboardingWindow.width,
        height: TrawlDesign.onboardingWindow.height
      )
    )
    host.frame.size = TrawlDesign.onboardingWindow
    host.layoutSubtreeIfNeeded()

    #expect(!containsScrollView(in: host))
  }

  @Test func onboardingViewsUseSemanticTypographyOnly() throws {
    let source = try String(
      contentsOf: URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("Sources/Trawl/OnboardingView.swift"),
      encoding: .utf8
    )

    #expect(!source.contains(".font("))
    #expect(!source.contains(".weight("))
    #expect(!source.contains(".bold("))
  }

  @MainActor
  @Test func returningLaunchUsesTheSameFixedWindow() {
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: TrawlDesign.minimumWindow),
      styleMask: [.titled, .closable, .miniaturizable, .resizable],
      backing: .buffered,
      defer: false
    )

    WindowBehavior.Coordinator().apply(isOnboarding: false, to: window)

    #expect(window.contentLayoutRect.size == TrawlDesign.defaultWindow)
    #expect(!window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.defaultWindow)
    #expect(window.contentMaxSize == TrawlDesign.defaultWindow)
  }

  @MainActor
  @Test func returningHomeMarksAbsentAppsNotInstalled() async throws {
    let client = RootViewStatusClient(response: try productStatusWithMissingWhatsApp().model())
    let model = AppModel(
      client: client,
      permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
    )
    await model.refresh()
    let installations = MacAppInstallations(
      environment: [:],
      applicationIsInstalled: { $0 != "net.whatsapp.WhatsApp" }
    )
    installations.refresh(manifests: model.sources.map(\.manifest))

    let overrides = HomeSourcePresentation.detailOverrides(
      for: model.restingSources,
      appInstallations: installations
    )

    #expect(overrides == ["whatsapp": OperationalCopy.AppStatus.notInstalled])
    #expect(model.restingSources.first(where: { $0.id == "whatsapp" })?.detail == "Not set up.")
  }

  @MainActor
  @Test func photosSetupRequirementDoesNotAddGlobalHomeChrome() async throws {
    let client = RootViewStatusClient(response: try productStatusWithPhotosSetup().model())
    let model = AppModel(
      client: client,
      permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
    )
    await model.refresh()

    let defaults = try #require(UserDefaults(suiteName: #function))
    defer { defaults.removePersistentDomain(forName: #function) }
    defaults.set(true, forKey: OnboardingModel.completionKey)
    let onboarding = OnboardingModel(defaults: defaults)

    let host = NSHostingView(
      rootView: RootView(
        model: model,
        client: client,
        onboarding: onboarding
      ))
    let window = NSWindow(
      contentRect: NSRect(x: 0, y: 0, width: 800, height: 700),
      styleMask: [.titled],
      backing: .buffered,
      defer: false
    )
    window.contentView = host
    window.makeKeyAndOrderFront(nil)
    defer { window.orderOut(nil) }

    host.layoutSubtreeIfNeeded()
    try await Task.sleep(for: .milliseconds(50))

    let sourceHosts = sourceHostingViews(in: host)
    let renderedSources = sourceHosts.flatMap { restingSources(in: $0.rootView) }
    let expectedSourceIDs: Set<String> = [
      "calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter",
      "whatsapp",
    ]

    #expect(model.photosAccess?.action == .requestPhotos)
    #expect(sourceHosts.count == expectedSourceIDs.count)
    #expect(renderedSources.count == expectedSourceIDs.count)
    #expect(Set(renderedSources.map(\.id)) == expectedSourceIDs)

    let mountedBody = host.rootView.body
    #expect(containsConcreteView(named: "ConstellationView", in: mountedBody))
    #expect(!containsConcreteView(named: "PhotosPermissionBanner", in: mountedBody))
  }
}

@MainActor
private func containsScrollView(in view: NSView) -> Bool {
  if view is NSScrollView { return true }
  return view.subviews.contains(where: containsScrollView)
}

private struct RootViewStatusClient: TrawlClient {
  let response: StatusResponse

  func status() async throws -> StatusResponse { response }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

@MainActor
private func sourceHostingViews(in view: NSView) -> [NSHostingView<AnyView>] {
  let current = (view as? NSHostingView<AnyView>).map { [$0] } ?? []
  return current + view.subviews.flatMap(sourceHostingViews)
}

private func restingSources(in value: Any, depth: Int = 0) -> [RestingSource] {
  guard depth < 24 else { return [] }
  if let source = value as? RestingSource { return [source] }
  return Mirror(reflecting: value).children.flatMap {
    restingSources(in: $0.value, depth: depth + 1)
  }
}

private func containsConcreteView(named name: String, in value: Any, depth: Int = 0) -> Bool {
  guard depth < 48 else { return false }
  if String(reflecting: type(of: value)).hasSuffix(name) { return true }
  return Mirror(reflecting: value).children.contains {
    containsConcreteView(named: name, in: $0.value, depth: depth + 1)
  }
}

private func productStatusWithPhotosSetup() -> Trawl_Federation_V1_StatusResponse {
  .with {
    $0.outcome = .complete
    $0.sources = [
      source("calendar", "Calendar"),
      source("contacts", "Contacts"),
      source("gmail", "Gmail"),
      source("imessage", "Messages"),
      source("notes", "Notes"),
      source("photos", "Photos", needsPhotosAccess: true),
      source("telegram", "Telegram"),
      source("twitter", "Twitter (X)"),
      source("whatsapp", "WhatsApp"),
    ]
  }
}

private func productStatusWithMissingWhatsApp() -> Trawl_Federation_V1_StatusResponse {
  .with {
    $0.outcome = .complete
    $0.sources = [
      source("contacts", "Contacts"),
      source("imessage", "Messages"),
      source("notes", "Notes"),
      source("telegram", "Telegram"),
      source(
        "whatsapp",
        "WhatsApp",
        state: "missing",
        bundleIdentifier: "net.whatsapp.WhatsApp"
      ),
    ]
  }
}

private func source(
  _ id: String,
  _ surface: String,
  state: String = "ok",
  needsPhotosAccess: Bool = false,
  bundleIdentifier: String? = nil
) -> Trawl_Federation_V1_SourceStatus {
  .with {
    $0.manifest = .with {
      $0.sourceID = id
      $0.displayName = surface
      if let bundleIdentifier {
        $0.branding = .with { $0.bundleIdentifier = bundleIdentifier }
      }
    }
    $0.state = state
    if needsPhotosAccess {
      $0.setupRequirements = [
        .with {
          $0.id = "photos_access"
          $0.kind = .photosPermission
          $0.state = .needsAction
          $0.explanation = "Photos access could not be checked."
          $0.action = .requestPhotos
        }
      ]
    }
  }
}
