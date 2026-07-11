import Foundation

public struct OpenedResult: Sendable, Equatable {
  public let rawOutput: Data
  public let text: String?

  public init(rawOutput: Data) {
    self.rawOutput = rawOutput
    text = String(data: rawOutput, encoding: .utf8)
  }

  public var hexadecimal: String {
    rawOutput.map { String(format: "%02x", $0) }.joined(separator: " ")
  }
}
