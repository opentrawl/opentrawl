import Foundation
import SwiftProtobuf

public enum DelimitedFrames {
  public static let maximumFrameBytes = 16 * 1024 * 1024

  public static func decode(_ data: Data) throws -> [Data] {
    var offset = data.startIndex
    var frames: [Data] = []

    while offset < data.endIndex {
      let length = try decodeLength(in: data, offset: &offset)
      guard length <= maximumFrameBytes else {
        throw TrawlClientError.frameTooLarge
      }
      guard length <= data.distance(from: offset, to: data.endIndex) else {
        throw TrawlClientError.invalidFrame
      }
      let end = data.index(offset, offsetBy: length)
      frames.append(data[offset..<end])
      offset = end
    }
    return frames
  }

  public static func encode<Message: SwiftProtobuf.Message>(_ message: Message) throws -> Data {
    let payload = try message.serializedData()
    guard payload.count <= maximumFrameBytes else {
      throw TrawlClientError.frameTooLarge
    }
    return encodeLength(payload.count) + payload
  }

  private static func encodeLength(_ length: Int) -> Data {
    var value = length
    var bytes: [UInt8] = []
    repeat {
      var byte = UInt8(value & 0x7f)
      value >>= 7
      if value > 0 { byte |= 0x80 }
      bytes.append(byte)
    } while value > 0
    return Data(bytes)
  }

  private static func decodeLength(in data: Data, offset: inout Data.Index) throws -> Int {
    var value: UInt64 = 0
    for shift in stride(from: 0, through: 63, by: 7) {
      guard offset < data.endIndex else {
        throw TrawlClientError.invalidFrame
      }
      let byte = data[offset]
      offset = data.index(after: offset)

      if shift == 63, byte > 1 {
        throw TrawlClientError.invalidFrame
      }
      value |= UInt64(byte & 0x7f) << UInt64(shift)
      if byte & 0x80 == 0 {
        guard value <= UInt64(Int.max) else {
          throw TrawlClientError.frameTooLarge
        }
        return Int(value)
      }
    }
    throw TrawlClientError.invalidFrame
  }
}
