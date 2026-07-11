import Foundation
import Testing

@testable import TrawlCore

@Test func openedResultKeepsValidUTF8Exactly() {
  let raw = Data("Source: Messages\nFrom: example@example.com\n\nSynthetic body\n".utf8)

  let result = OpenedResult(rawOutput: raw)

  #expect(result.rawOutput == raw)
  #expect(result.text == "Source: Messages\nFrom: example@example.com\n\nSynthetic body\n")
}

@Test func openedResultRepresentsInvalidUTF8Losslessly() {
  let raw = Data([0x00, 0xff, 0x41, 0x0a])

  let result = OpenedResult(rawOutput: raw)

  #expect(result.rawOutput == raw)
  #expect(result.text == nil)
  #expect(result.hexadecimal == "00 ff 41 0a")
}
