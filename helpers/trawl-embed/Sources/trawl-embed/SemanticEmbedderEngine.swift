import Foundation
import MLX
import MLXEmbedders
import MLXLMCommon
import Tokenizers

private struct AdaptedHuggingFaceTokenizer: MLXLMCommon.Tokenizer, @unchecked Sendable {
    let upstream: any Tokenizers.Tokenizer

    func encode(text: String, addSpecialTokens: Bool) -> [Int] {
        upstream.encode(text: text, addSpecialTokens: addSpecialTokens)
    }

    func decode(tokenIds: [Int], skipSpecialTokens: Bool) -> String {
        upstream.decode(tokens: tokenIds, skipSpecialTokens: skipSpecialTokens)
    }

    func convertTokenToId(_ token: String) -> Int? {
        upstream.convertTokenToId(token)
    }

    func convertIdToToken(_ id: Int) -> String? {
        upstream.convertIdToToken(id)
    }

    var bosToken: String? { upstream.bosToken }
    var eosToken: String? { upstream.eosToken }
    var unknownToken: String? { upstream.unknownToken }

    func applyChatTemplate(
        messages: [[String: any Sendable]],
        tools: [[String: any Sendable]]?,
        additionalContext: [String: any Sendable]?
    ) throws -> [Int] {
        throw MLXLMCommon.TokenizerError.missingChatTemplate
    }
}

private struct LocalDirectoryTokenizerLoader: TokenizerLoader {
    func load(from directory: URL) async throws -> any MLXLMCommon.Tokenizer {
        let tokenizer = try await AutoTokenizer.from(modelFolder: directory)
        return AdaptedHuggingFaceTokenizer(upstream: tokenizer)
    }
}

struct SemanticEmbedderHealthSnapshot: Sendable {
    let loadedModelIdentifier: String
    let modelCurrentlyLoaded: Bool
    let activeMetalMemoryBytes: UInt64
    let peakMetalMemoryBytes: UInt64
}

struct SemanticEmbedderBatchOutput: Sendable {
    let modelIdentifier: String
    let outputDimensions: Int
    let quantizedInt8Vectors: [Data]
    let elapsedMilliseconds: UInt64
}

/// Serializes all Apple Silicon MLX embedding work inside one actor so
/// concurrent agents never stack multiple GPU allocations on 16 GB Macs.
actor SemanticEmbedderEngine {
    private static let queryInstructionPrefix =
        "Instruct: Given a personal archive search query, retrieve relevant messages, emails, notes, and events\nQuery: "
    private static let maxBatchSize = 16

    private var loadedModelDirectoryPath: String = ""
    private var loadedModelIdentifier: String = ""
    private var loadedContainer: EmbedderModelContainer?
    private var loadedEOSTokenID: Int = 151643
    private var lastActivityTime: Date = Date()

    init() {
        // Bound MLX buffer cache to keep peak memory predictable on 16 GB Macs.
        MLX.Memory.cacheLimit = 256 * 1024 * 1024
    }

    func healthSnapshot() -> SemanticEmbedderHealthSnapshot {
        SemanticEmbedderHealthSnapshot(
            loadedModelIdentifier: loadedModelIdentifier,
            modelCurrentlyLoaded: loadedContainer != nil,
            activeMetalMemoryBytes: UInt64(max(0, MLX.Memory.activeMemory)),
            peakMetalMemoryBytes: UInt64(max(0, MLX.Memory.peakMemory))
        )
    }

    func secondsSinceLastActivity() -> TimeInterval {
        Date().timeIntervalSince(lastActivityTime)
    }

    func unloadModelIfIdle(idleSeconds: TimeInterval) -> Bool {
        guard loadedContainer != nil else { return false }
        guard Date().timeIntervalSince(lastActivityTime) >= idleSeconds else { return false }
        unloadModel()
        return true
    }

    func unloadModel() {
        loadedContainer = nil
        loadedModelDirectoryPath = ""
        loadedModelIdentifier = ""
        MLX.Memory.clearCache()
    }

    func embedTexts(
        localModelDirectoryPath: String,
        modelIdentifier: String,
        inputTexts: [String],
        outputDimensions: Int,
        maximumTokenCount: Int,
        inputRepresentsSearchQuery: Bool
    ) async throws -> SemanticEmbedderBatchOutput {
        lastActivityTime = Date()
        let started = CFAbsoluteTimeGetCurrent()
        let dims = outputDimensions > 0 ? outputDimensions : 512
        let maxTokens = maximumTokenCount > 0 ? maximumTokenCount : 256

        if inputTexts.isEmpty {
            return SemanticEmbedderBatchOutput(
                modelIdentifier: modelIdentifier,
                outputDimensions: dims,
                quantizedInt8Vectors: [],
                elapsedMilliseconds: 0
            )
        }

        let container = try await ensureModelLoaded(
            localModelDirectoryPath: localModelDirectoryPath,
            modelIdentifier: modelIdentifier
        )
        let eosTokenID = loadedEOSTokenID

        let preparedTexts: [String]
        if inputRepresentsSearchQuery {
            preparedTexts = inputTexts.map { Self.queryInstructionPrefix + $0 }
        } else {
            preparedTexts = inputTexts
        }

        // Tokenize and sort by token length to minimize padding waste across batches.
        let encodedWithIndex: [(index: Int, tokens: [Int])] = await container.perform { context in
            var pairs: [(Int, [Int])] = []
            pairs.reserveCapacity(preparedTexts.count)
            for (idx, text) in preparedTexts.enumerated() {
                var ids = context.tokenizer.encode(text: text, addSpecialTokens: false)
                if ids.count >= maxTokens {
                    ids = Array(ids.prefix(maxTokens - 1))
                }
                ids.append(eosTokenID)
                pairs.append((idx, ids))
            }
            return pairs
        }

        let sorted = encodedWithIndex.sorted { $0.tokens.count < $1.tokens.count }
        var outputVectors = Array(repeating: Data(), count: preparedTexts.count)

        var batchStart = 0
        while batchStart < sorted.count {
            let batchEnd = min(batchStart + Self.maxBatchSize, sorted.count)
            let slice = Array(sorted[batchStart..<batchEnd])
            let batchVectors: [Data] = await container.perform { context in
                let maxLength = slice.map { $0.tokens.count }.max() ?? 1
                let paddedRows = slice.map { entry in
                    MLXArray(entry.tokens + Array(repeating: eosTokenID, count: maxLength - entry.tokens.count))
                }
                let maskRows = slice.map { entry in
                    MLXArray(
                        Array(repeating: true, count: entry.tokens.count)
                            + Array(repeating: false, count: maxLength - entry.tokens.count)
                    )
                }
                let padded = stacked(paddedRows)
                let mask = stacked(maskRows)
                let output = context.model(padded, positionIds: nil, tokenTypeIds: nil, attentionMask: mask)
                let pooled = context.pooling(output, mask: mask, normalize: false, applyLayerNorm: false)
                let actualDims = min(dims, pooled.shape[pooled.ndim - 1])
                let truncated = pooled[0..., 0..<actualDims]
                let norm = sqrt(sum(truncated * truncated, axis: -1, keepDims: true) + 1e-12)
                let normalized = truncated / norm
                eval(normalized)
                return (0..<slice.count).map { rowIndex in
                    let floats = normalized[rowIndex].asArray(Float.self)
                    return Self.quantizeUnitVectorToInt8(floats)
                }
            }
            for (offset, entry) in slice.enumerated() {
                outputVectors[entry.index] = batchVectors[offset]
            }
            batchStart = batchEnd
        }

        lastActivityTime = Date()
        let elapsedMs = UInt64(max(0, (CFAbsoluteTimeGetCurrent() - started) * 1000.0))
        return SemanticEmbedderBatchOutput(
            modelIdentifier: loadedModelIdentifier,
            outputDimensions: dims,
            quantizedInt8Vectors: outputVectors,
            elapsedMilliseconds: elapsedMs
        )
    }

    private func ensureModelLoaded(
        localModelDirectoryPath: String,
        modelIdentifier: String
    ) async throws -> EmbedderModelContainer {
        if let existing = loadedContainer, loadedModelDirectoryPath == localModelDirectoryPath {
            return existing
        }
        unloadModel()
        let modelURL = URL(filePath: localModelDirectoryPath)
        guard FileManager.default.fileExists(atPath: modelURL.path) else {
            throw NSError(
                domain: "TrawlEmbed",
                code: 2,
                userInfo: [NSLocalizedDescriptionKey: "Local model directory does not exist: \(localModelDirectoryPath)"]
            )
        }
        let container = try await EmbedderModelFactory.shared.loadContainer(
            from: modelURL,
            using: LocalDirectoryTokenizerLoader()
        )
        let configEOS = Self.readEOSTokenIDFromModelConfig(modelDirectory: modelURL)
        let resolvedEOS: Int = await container.perform { context in
            if let configEOS {
                return configEOS
            }
            return context.tokenizer.convertTokenToId("<|endoftext|>")
                ?? context.tokenizer.eosTokenId
                ?? 151643
        }
        loadedContainer = container
        loadedModelDirectoryPath = localModelDirectoryPath
        loadedModelIdentifier = modelIdentifier.isEmpty ? modelURL.lastPathComponent : modelIdentifier
        loadedEOSTokenID = resolvedEOS
        return container
    }

    private static func readEOSTokenIDFromModelConfig(modelDirectory: URL) -> Int? {
        let configURL = modelDirectory.appending(component: "config.json")
        guard let data = try? Data(contentsOf: configURL),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else {
            return nil
        }
        if let single = json["eos_token_id"] as? Int {
            return single
        }
        if let list = json["eos_token_id"] as? [Int], let first = list.first {
            return first
        }
        return nil
    }

    private static func quantizeUnitVectorToInt8(_ values: [Float]) -> Data {
        var bytes = [UInt8](repeating: 0, count: values.count)
        for index in 0..<values.count {
            let clamped = max(-1.0 as Float, min(1.0 as Float, values[index]))
            let scaled = Int(roundf(clamped * 127.0))
            let signed = Int8(max(-127, min(127, scaled)))
            bytes[index] = UInt8(bitPattern: signed)
        }
        return Data(bytes)
    }
}
