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
  @Test func photosSetupRequirementDoesNotAddGlobalHomeChrome() async throws {
    let client = RootViewStatusClient(response: try productStatusWithPhotosSetup().model())
    let model = AppModel(
      client: client,
      permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
    )
    await model.refresh()

    let host = NSHostingView(rootView: RootView(model: model, client: client))
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

private struct RootViewStatusClient: TrawlClient {
  let response: StatusResponse

  func status() async throws -> StatusResponse { response }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String) async throws -> OpenResponse { fatalError() }
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
  guard depth < 32 else { return false }
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
      source("twitter", "X"),
      source("whatsapp", "WhatsApp"),
    ]
  }
}

private func source(
  _ id: String,
  _ surface: String,
  needsPhotosAccess: Bool = false
) -> Trawl_Federation_V1_SourceStatus {
  .with {
    $0.manifest = .with {
      $0.sourceID = id
      $0.surface = surface
    }
    $0.state = "ok"
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
