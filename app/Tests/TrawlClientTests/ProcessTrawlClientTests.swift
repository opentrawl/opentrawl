import Foundation
import Testing

@testable import TrawlClient

@Test func runtimeConfigurationKeepsProductionDefaultsAndQuotesAnIsolatedAlpha() {
  let production = TrawlRuntimeConfiguration(
    bundleURL: URL(fileURLWithPath: "/Applications/OpenTrawl.app"),
    environment: [:]
  )
  #expect(production.stateRoot == nil)
  #expect(production.helperURL.path == "/Applications/OpenTrawl.app/Contents/Helpers/trawl")
  #expect(production.agentCommand == "/Applications/OpenTrawl.app/Contents/Helpers/trawl")

  let alpha = TrawlRuntimeConfiguration(
    bundleURL: URL(fileURLWithPath: "/Applications/OpenTrawl Alpha.app"),
    environment: [TrawlRuntimeConfiguration.stateRootEnvironmentKey: "/tmp/OpenTrawl Alpha"]
  )
  #expect(alpha.stateRoot == "/tmp/OpenTrawl Alpha")
  #expect(
    alpha.agentCommand
      == "env OPENTRAWL_STATE_ROOT='/tmp/OpenTrawl Alpha' '/Applications/OpenTrawl Alpha.app/Contents/Helpers/trawl'"
  )
}

@Test func processClientInvokesCurrentGoHelperForStatusAndSearch() async throws {
  let environment = ProcessInfo.processInfo.environment
  guard let helperPath = environment["OPENTRAWL_TEST_TRAWL"], !helperPath.isEmpty else {
    Issue.record("scripts/check-test must provide the current Go trawl helper")
    return
  }
  guard let isolatedRoot = environment["OPENTRAWL_TEST_STATE_ROOT"], !isolatedRoot.isEmpty else {
    Issue.record("scripts/check-test must provide an isolated OpenTrawl state root")
    return
  }
  let helper = URL(fileURLWithPath: helperPath)
  let receipts = ReceiptStore()
  let client = ProcessTrawlClient(binaryURL: helper) { receipt in
    receipts.append(receipt)
  }

  let status = try await client.status()
  let search = try await client.search("Avery", source: "contacts")

  #expect(status.sources.contains { $0.manifest.sourceID == "contacts" })
  #expect(search.outcome == .complete)
  #expect(search.resultLimit == 20)
  #expect(search.sources.map(\.sourceID) == ["contacts"])
  #expect(search.hits.count == 1)
  #expect(search.hits[0].sourceID == "contacts")
  #expect(search.hits[0].summary.title == "Avery Example")
  #expect(search.hits[0].openRef.hasPrefix("contacts:person/"))
  #expect(!search.hits[0].evidence.isEmpty)

  let captured = receipts.values
  #expect(captured.count == 2)
  #expect(captured.map(\.executableURL) == [helper, helper])
  #expect(
    captured.map(\.arguments) == [
      ["__app", "status"], ["__app", "search", "--source", "contacts", "Avery"],
    ])
  #expect(captured.allSatisfy { $0.stdin.isEmpty && !$0.stdout.isEmpty && $0.exitCode == 0 })
  for receipt in captured {
    #expect(throws: Never.self) { try DelimitedFrames.decodeExactlyOne(receipt.stdout) }
  }

  let isolatedReceipts = ReceiptStore()
  let isolatedClient = ProcessTrawlClient(
    binaryURL: helper,
    stateRoot: isolatedRoot,
    receiveReceipt: { isolatedReceipts.append($0) }
  )
  let isolatedStatus = try await isolatedClient.status()
  let isolatedSearch = try await isolatedClient.search("Jordan", source: "contacts")
  let isolatedHit = try #require(isolatedSearch.hits.first)
  let isolatedOpen = try await isolatedClient.open(
    sourceID: isolatedHit.sourceID,
    ref: isolatedHit.openRef,
    anchorID: isolatedHit.anchorID
  )
  let productionOnlySearch = try await isolatedClient.search("Avery", source: "contacts")
  let isolatedOnlySearch = try await client.search("Jordan", source: "contacts")

  let contacts = try #require(isolatedStatus.sources.first { $0.id == "contacts" })
  let isolatedContactsPath = URL(fileURLWithPath: isolatedRoot)
    .appending(path: "contacts/contacts.db").standardizedFileURL.path
  #expect(contacts.databasePath == isolatedContactsPath)
  #expect(isolatedHit.summary.title == "Jordan Isolated")
  #expect(isolatedOpen.record?.sourceID == "contacts")
  #expect(productionOnlySearch.hits.isEmpty)
  #expect(isolatedOnlySearch.hits.isEmpty)
  #expect(isolatedReceipts.values.allSatisfy { $0.stateRoot == isolatedRoot })
}

private final class ReceiptStore: @unchecked Sendable {
  private let lock = NSLock()
  private var receipts: [ProcessBoundaryReceipt] = []

  var values: [ProcessBoundaryReceipt] {
    lock.withLock { receipts }
  }

  func append(_ receipt: ProcessBoundaryReceipt) {
    lock.withLock { receipts.append(receipt) }
  }
}
