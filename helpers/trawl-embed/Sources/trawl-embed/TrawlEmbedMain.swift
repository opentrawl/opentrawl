import Foundation
import MLX

@main
struct TrawlEmbedMain {
    static func main() async {
        configureBundledMetalLibraryPath()
        let arguments = CommandLine.arguments
        var socketPath = ""
        var idleUnloadSeconds: Double = 90
        var idleExitSeconds: Double = 600

        var index = 1
        while index < arguments.count {
            let arg = arguments[index]
            switch arg {
            case "--socket":
                if index + 1 < arguments.count {
                    socketPath = arguments[index + 1]
                    index += 2
                    continue
                }
            case "--idle-unload-seconds":
                if index + 1 < arguments.count, let parsed = Double(arguments[index + 1]) {
                    idleUnloadSeconds = parsed
                    index += 2
                    continue
                }
            case "--idle-exit-seconds":
                if index + 1 < arguments.count, let parsed = Double(arguments[index + 1]) {
                    idleExitSeconds = parsed
                    index += 2
                    continue
                }
            case "--version":
                print("trawl-embed")
                return
            default:
                break
            }
            index += 1
        }

        if socketPath.isEmpty {
            fputs("usage: trawl-embed --socket PATH [--idle-unload-seconds 90] [--idle-exit-seconds 600]\n", stderr)
            Foundation.exit(2)
        }

        let engine = SemanticEmbedderEngine()
        let server = UnixSocketProtobufServer(
            socketPath: socketPath,
            engine: engine,
            idleUnloadSeconds: idleUnloadSeconds,
            idleExitSeconds: idleExitSeconds
        )
        do {
            try await server.run()
        } catch {
            fputs("trawl-embed: \(error.localizedDescription)\n", stderr)
            Foundation.exit(1)
        }
    }

    private static func configureBundledMetalLibraryPath() {
        let executableURL = URL(filePath: CommandLine.arguments[0]).resolvingSymlinksInPath()
        let executableDir = executableURL.deletingLastPathComponent()
        let candidates = [
            executableDir.appending(components: "..", "Resources", "mlx.metallib").standardizedFileURL,
            executableDir.appending(component: "mlx.metallib"),
            executableDir.appending(components: "mlx-swift_Cmlx.bundle", "Contents", "Resources", "default.metallib"),
        ]
        for candidate in candidates where FileManager.default.fileExists(atPath: candidate.path) {
            MLX.GPU.metallib = candidate
            return
        }
    }
}
