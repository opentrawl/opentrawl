import Foundation
import Observation
import PermissionGuide
import TrawlClient

public enum HomePhase: Sendable, Equatable {
  case loading
  case ready
  case failed(String)
}

@MainActor
@Observable
public final class AppModel {
  private let client: any TrawlClient
  private let permissionProbe: FullDiskAccessProbe

  public private(set) var phase: HomePhase = .loading
  public private(set) var sources: [SourceStatus] = []
  public private(set) var completion: FanoutCompletion = .complete
  public private(set) var isSyncing = false
  public private(set) var syncMessage: String?
  public private(set) var diskAccess: FullDiskAccessStatus = .undetermined

  public init(
    client: any TrawlClient,
    permissionProbe: FullDiskAccessProbe = FullDiskAccessProbe()
  ) {
    self.client = client
    self.permissionProbe = permissionProbe
  }

  public func refresh() async {
    diskAccess = permissionProbe.status()
    if sources.isEmpty {
      phase = .loading
    }
    do {
      let response = try await client.status()
      sources = response.sources
      completion = response.completion
      phase = .ready
    } catch is CancellationError {
      return
    } catch {
      phase = .failed(error.localizedDescription)
    }
  }

  public func syncNow() async {
    guard !isSyncing else { return }
    isSyncing = true
    syncMessage = nil
    defer { isSyncing = false }

    do {
      let result = try await client.sync()
      switch result {
      case .complete:
        break
      case .partial:
        syncMessage = "Some sources could not sync."
      case .failed:
        syncMessage = "No source could sync."
      }
      await refresh()
    } catch is CancellationError {
      return
    } catch {
      syncMessage = error.localizedDescription
    }
  }

  public func permissionChanged() async {
    diskAccess = permissionProbe.status()
    await refresh()
  }
}
