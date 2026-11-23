import Darwin
import Foundation
import SwiftProtobuf

enum WireFramingError: Error, LocalizedError {
    case endOfStream
    case invalidVarint
    case messageTooLarge(Int)
    case socketFailure(String)

    var errorDescription: String? {
        switch self {
        case .endOfStream:
            return "connection closed"
        case .invalidVarint:
            return "invalid length-delimited varint prefix"
        case .messageTooLarge(let size):
            return "wire message exceeds limit (\(size) bytes)"
        case .socketFailure(let detail):
            return detail
        }
    }
}

private enum SocketFraming {
    private static let maximumFrameBytes = 32 * 1024 * 1024

    static func readDelimitedMessage<M: SwiftProtobuf.Message>(from fd: Int32, as type: M.Type) throws -> M {
        let length = try readVarint(from: fd)
        guard length >= 0, length <= maximumFrameBytes else {
            throw WireFramingError.messageTooLarge(length)
        }
        let payload = try readExactBytes(count: length, from: fd)
        return try M(serializedBytes: payload)
    }

    static func writeDelimitedMessage<M: SwiftProtobuf.Message>(_ message: M, to fd: Int32) throws {
        let payload = try message.serializedData()
        var framed = encodeVarint(UInt64(payload.count))
        framed.append(payload)
        try writeAllBytes(framed, to: fd)
    }

    private static func readVarint(from fd: Int32) throws -> Int {
        var result: UInt64 = 0
        var shift: UInt64 = 0
        var byte: UInt8 = 0
        while shift < 64 {
            let n = Darwin.read(fd, &byte, 1)
            if n == 0 {
                throw WireFramingError.endOfStream
            }
            if n < 0 {
                if errno == EINTR { continue }
                throw WireFramingError.socketFailure("socket read failed: errno \(errno)")
            }
            result |= UInt64(byte & 0x7F) << shift
            if (byte & 0x80) == 0 {
                return Int(result)
            }
            shift += 7
        }
        throw WireFramingError.invalidVarint
    }

    private static func encodeVarint(_ value: UInt64) -> Data {
        var v = value
        var out = Data()
        while true {
            var byte = UInt8(v & 0x7F)
            v >>= 7
            if v != 0 {
                byte |= 0x80
                out.append(byte)
            } else {
                out.append(byte)
                break
            }
        }
        return out
    }

    private static func readExactBytes(count: Int, from fd: Int32) throws -> Data {
        if count == 0 { return Data() }
        var buffer = [UInt8](repeating: 0, count: count)
        var offset = 0
        while offset < count {
            let n = buffer.withUnsafeMutableBytes { raw -> Int in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.read(fd, base.advanced(by: offset), count - offset)
            }
            if n == 0 {
                throw WireFramingError.endOfStream
            }
            if n < 0 {
                if errno == EINTR { continue }
                throw WireFramingError.socketFailure("socket read failed: errno \(errno)")
            }
            offset += n
        }
        return Data(buffer)
    }

    private static func writeAllBytes(_ data: Data, to fd: Int32) throws {
        var offset = 0
        let total = data.count
        while offset < total {
            let n = data.withUnsafeBytes { raw -> Int in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.write(fd, base.advanced(by: offset), total - offset)
            }
            if n < 0 {
                if errno == EINTR { continue }
                throw WireFramingError.socketFailure("socket write failed: errno \(errno)")
            }
            offset += n
        }
    }
}

final class UnixSocketProtobufServer: @unchecked Sendable {
    private let socketPath: String
    private let engine: SemanticEmbedderEngine
    private let idleUnloadSeconds: TimeInterval
    private let idleExitSeconds: TimeInterval
    private var listenFD: Int32 = -1
    private var stopRequested = false

    init(
        socketPath: String,
        engine: SemanticEmbedderEngine,
        idleUnloadSeconds: TimeInterval,
        idleExitSeconds: TimeInterval
    ) {
        self.socketPath = socketPath
        self.engine = engine
        self.idleUnloadSeconds = idleUnloadSeconds
        self.idleExitSeconds = idleExitSeconds
    }

    func run() async throws {
        signal(SIGPIPE, SIG_IGN)
        let parentURL = URL(filePath: socketPath).deletingLastPathComponent()
        try FileManager.default.createDirectory(at: parentURL, withIntermediateDirectories: true)
        unlink(socketPath)

        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            throw WireFramingError.socketFailure("socket() failed: errno \(errno)")
        }
        listenFD = fd
        defer { cleanupSocket() }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8)
        guard pathBytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            throw WireFramingError.socketFailure("Unix socket path too long: \(socketPath)")
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { raw in
            for (index, byte) in pathBytes.enumerated() {
                raw[index] = byte
            }
            raw[pathBytes.count] = 0
        }

        let bindResult = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockPtr in
                Darwin.bind(fd, sockPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bindResult == 0 else {
            throw WireFramingError.socketFailure("bind(\(socketPath)) failed: errno \(errno)")
        }
        chmod(socketPath, 0o600)
        guard Darwin.listen(fd, 16) == 0 else {
            throw WireFramingError.socketFailure("listen() failed: errno \(errno)")
        }

        while !stopRequested {
            var pollDescriptor = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
            let ready = Darwin.poll(&pollDescriptor, 1, 1000)
            if ready < 0 {
                if errno == EINTR { continue }
                throw WireFramingError.socketFailure("poll() failed: errno \(errno)")
            }
            if ready == 0 {
                let idle = await engine.secondsSinceLastActivity()
                if idleUnloadSeconds > 0 {
                    _ = await engine.unloadModelIfIdle(idleSeconds: idleUnloadSeconds)
                }
                if idleExitSeconds > 0 && idle >= idleExitSeconds {
                    break
                }
                continue
            }
            let clientFD = Darwin.accept(fd, nil, nil)
            if clientFD < 0 {
                if errno == EINTR { continue }
                continue
            }
            await handleConnection(clientFD: clientFD)
            Darwin.close(clientFD)
        }
    }

    private func handleConnection(clientFD: Int32) async {
        while !stopRequested {
            let request: Trawl_Semantic_SemanticEmbedderRequest
            do {
                request = try SocketFraming.readDelimitedMessage(
                    from: clientFD,
                    as: Trawl_Semantic_SemanticEmbedderRequest.self
                )
            } catch WireFramingError.endOfStream {
                return
            } catch {
                var failure = Trawl_Semantic_SemanticEmbedderResponse()
                failure.failure.failureMessage = error.localizedDescription
                try? SocketFraming.writeDelimitedMessage(failure, to: clientFD)
                return
            }

            let response = await dispatch(request: request)
            do {
                try SocketFraming.writeDelimitedMessage(response, to: clientFD)
            } catch {
                return
            }
        }
    }

    private func dispatch(request: Trawl_Semantic_SemanticEmbedderRequest) async -> Trawl_Semantic_SemanticEmbedderResponse {
        var response = Trawl_Semantic_SemanticEmbedderResponse()
        switch request.requestPayload {
        case .healthCheck:
            let snap = await engine.healthSnapshot()
            response.health.loadedModelIdentifier = snap.loadedModelIdentifier
            response.health.modelCurrentlyLoaded = snap.modelCurrentlyLoaded
            response.health.activeMetalMemoryBytes = snap.activeMetalMemoryBytes
            response.health.peakMetalMemoryBytes = snap.peakMetalMemoryBytes
        case .embedTexts(let embedReq):
            do {
                let output = try await engine.embedTexts(
                    localModelDirectoryPath: embedReq.localModelDirectoryPath,
                    modelIdentifier: embedReq.modelIdentifier,
                    inputTexts: embedReq.inputTexts,
                    outputDimensions: Int(embedReq.outputEmbeddingDimensions),
                    maximumTokenCount: Int(embedReq.maximumInputTokenCount),
                    inputRepresentsSearchQuery: embedReq.inputRepresentsSearchQuery
                )
                response.embeddings.modelIdentifier = output.modelIdentifier
                response.embeddings.outputEmbeddingDimensions = UInt32(output.outputDimensions)
                response.embeddings.quantizedInt8Vectors = output.quantizedInt8Vectors
                response.embeddings.elapsedMilliseconds = output.elapsedMilliseconds
            } catch {
                response.failure.failureMessage = error.localizedDescription
            }
        case .unloadModel:
            await engine.unloadModel()
            response.acknowledged = Trawl_Semantic_SemanticEmbedderAcknowledgedResponse()
        case .shutdown:
            await engine.unloadModel()
            stopRequested = true
            response.acknowledged = Trawl_Semantic_SemanticEmbedderAcknowledgedResponse()
        case .none:
            response.failure.failureMessage = "empty semantic embedder request"
        }
        return response
    }

    private func cleanupSocket() {
        if listenFD >= 0 {
            Darwin.close(listenFD)
            listenFD = -1
        }
        unlink(socketPath)
    }
}
