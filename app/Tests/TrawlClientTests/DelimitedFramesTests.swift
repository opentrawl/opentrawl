import Foundation
import SwiftProtobuf
import Testing

@testable import TrawlClient

@Test func decodesMoreThanOneDelimitedFrame() throws {
  let first = Data([1, 2, 3])
  let second = Data([4, 5])

  let frames = try DelimitedFrames.decode(framed(first) + framed(second))

  #expect(frames == [first, second])
}

@Test func rejectsTruncatedAndOversizedFrames() throws {
  #expect(throws: TrawlClientError.invalidFrame) {
    try DelimitedFrames.decode(Data([3, 1]))
  }
  let tooLarge = varint(DelimitedFrames.maximumFrameBytes + 1)
  #expect(throws: TrawlClientError.frameTooLarge) {
    try DelimitedFrames.decode(tooLarge)
  }
}

@Test func processClientRetainsPartialSearchAndExactOpenOutput() async throws {
  var hit = Trawl_App_V1_SearchHit()
  hit.openRef = "gmail:message:example-1"
  hit.appID = "gmail"
  hit.title = "Example sender"
  hit.snippet = "Synthetic result"
  hit.whenDisplay = "10 Jul"
  let searchBytes = framed(try hit.serializedData())

  let binary = try fixtureBinary(
    searchBytes: searchBytes,
    searchExit: 3,
    openOutput: "first line\nsecond line\n"
  )
  defer { try? FileManager.default.removeItem(at: binary.deletingLastPathComponent()) }

  let client = ProcessTrawlClient(binaryURL: binary)
  let response = try await client.search("synthetic", source: nil)
  let output = try await client.open("gmail:message:example-1")

  #expect(response.completion == .partial)
  #expect(
    response.hits == [
      SearchHit(
        id: "gmail:message:example-1",
        sourceID: "gmail",
        title: "Example sender",
        snippet: "Synthetic result",
        whenDisplay: "10 Jul"
      )
    ])
  #expect(output == "first line\nsecond line\n")
}

@Test func processClientDistinguishesTotalFailureFromNoMatches() async throws {
  let failedBinary = try fixtureBinary(searchBytes: Data(), searchExit: 1, openOutput: "")
  defer { try? FileManager.default.removeItem(at: failedBinary.deletingLastPathComponent()) }
  let emptyBinary = try fixtureBinary(searchBytes: Data(), searchExit: 0, openOutput: "")
  defer { try? FileManager.default.removeItem(at: emptyBinary.deletingLastPathComponent()) }

  let failed = try await ProcessTrawlClient(binaryURL: failedBinary).search("none", source: nil)
  let empty = try await ProcessTrawlClient(binaryURL: emptyBinary).search("none", source: nil)

  #expect(failed.completion == .failed)
  #expect(failed.hits.isEmpty)
  #expect(empty.completion == .complete)
  #expect(empty.hits.isEmpty)
}

private func fixtureBinary(searchBytes: Data, searchExit: Int, openOutput: String) throws -> URL {
  let directory = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString, isDirectory: true)
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  let binary = directory.appendingPathComponent("trawl")
  let script = """
    #!/bin/sh
    if [ "$1" = "__app" ] && [ "$2" = "search" ]; then
      /usr/bin/printf '\(octal(searchBytes))'
      /usr/bin/printf 'one synthetic source failed\\n' >&2
      exit \(searchExit)
    fi
    if [ "$1" = "open" ]; then
      /usr/bin/printf '\(shellEscaped(openOutput))'
      exit 0
    fi
    exit 0
    """
  try Data(script.utf8).write(to: binary)
  try FileManager.default.setAttributes(
    [.posixPermissions: 0o755],
    ofItemAtPath: binary.path
  )
  return binary
}

private func framed(_ payload: Data) -> Data {
  varint(payload.count) + payload
}

private func varint(_ value: Int) -> Data {
  var value = value
  var bytes: [UInt8] = []
  repeat {
    var byte = UInt8(value & 0x7f)
    value >>= 7
    if value > 0 { byte |= 0x80 }
    bytes.append(byte)
  } while value > 0
  return Data(bytes)
}

private func octal(_ data: Data) -> String {
  data.map { String(format: "\\%03o", $0) }.joined()
}

private func shellEscaped(_ value: String) -> String {
  Data(value.utf8).map { String(format: "\\%03o", $0) }.joined()
}
