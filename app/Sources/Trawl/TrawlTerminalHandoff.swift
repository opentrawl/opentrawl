import Foundation

/// Builds the one-line Terminal command that opens the packaged `trawl`
/// command-line tool folder and prints its help.
enum TrawlTerminalHandoff {
  static func executableHelpCommand(helperURL: URL) -> String {
    let helperDirectory = shellQuoted(helperURL.deletingLastPathComponent().path)
    let relativeExecutable = shellQuoted("./\(helperURL.lastPathComponent)")
    return "cd \(helperDirectory) && \(relativeExecutable) --help"
  }

  private static func shellQuoted(_ argument: String) -> String {
    "'\(argument.replacingOccurrences(of: "'", with: "'\"'\"'"))'"
  }
}
