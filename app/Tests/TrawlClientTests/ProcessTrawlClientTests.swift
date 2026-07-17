import Foundation
import Testing

@testable import TrawlClient

@Test func processClientInvokesCurrentGoHelperForStatusAndSearch() async throws {
  let environment = ProcessInfo.processInfo.environment
  guard let helperPath = environment["OPENTRAWL_TEST_TRAWL"], !helperPath.isEmpty else {
    Issue.record("scripts/check-test must provide the current Go trawl helper")
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
