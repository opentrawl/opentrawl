import Foundation
import SwiftProtobuf

public enum DelimitedFrames {
  public static let maximumFrameBytes = 16 * 1024 * 1024
  private static let headerBytes = MemoryLayout<UInt32>.size

  public static func decodeExactlyOne(_ data: Data) throws -> Data {
    guard !data.isEmpty else {
      throw TrawlClientError.missingFrame
    }
    guard data.count >= headerBytes else {
      throw TrawlClientError.invalidFrame
    }

    let payloadLength = data.prefix(headerBytes).withUnsafeBytes { bytes in
      Int(UInt32(littleEndian: bytes.loadUnaligned(as: UInt32.self)))
    }
    guard payloadLength <= maximumFrameBytes else {
      throw TrawlClientError.oversizedFrame
    }

    let frameEnd = headerBytes + payloadLength
    guard data.count >= frameEnd else {
      throw TrawlClientError.invalidFrame
    }
    guard data.count == frameEnd else {
      throw TrawlClientError.extraFrame
    }
    return data.dropFirst(headerBytes)
  }

  public static func encode<Message: SwiftProtobuf.Message>(_ message: Message) throws -> Data {
    let payload = try message.serializedData()
    guard payload.count <= maximumFrameBytes else {
      throw TrawlClientError.oversizedFrame
    }
    var length = UInt32(payload.count).littleEndian
    return withUnsafeBytes(of: &length) { Data($0) } + payload
  }
}
