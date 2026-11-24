import Foundation

// IMPORTANT: EVERY PROMPT IN THIS FILE MUST FOLLOW THE OFFICIAL GPT-5.6
// PROMPTING GUIDE AND STATE ITS ACTUAL INTENT INSIDE THE PROMPT. PROMPTS MUST
// BE CLEAR ENOUGH FOR AN AI TO UNDERSTAND AND PERFORM THE INTENDED TASK. DO NOT
// ADD VAGUE, DECORATIVE OR MISLEADING PROMPTS.
enum AgentPrompts {
  static func auditBuild(_ identity: BuildIdentity) -> String {
    let source =
      identity.sourceURL?.absoluteString
      ?? "https://github.com/opentrawl/opentrawl"
    let appBundlePath = Bundle.main.bundleURL.path
    let buildKind = identity.isDevelopmentBuild ? "development build" : "alpha build"
    let updateClaim =
      identity.isDevelopmentBuild
      ? "does not check for software updates automatically"
      : "checks GitHub automatically for software updates"
    let buildDescription =
      if identity.hasLocalChanges {
        "This local build is based on Git commit \(identity.gitCommit) and includes uncommitted changes. The link shows the base code:"
      } else {
        "This is OpenTrawl \(identity.version), built from Git commit \(identity.gitCommit):"
      }
    let auditTarget =
      if identity.hasLocalChanges {
        "Check those claims against this app bundle and the linked base commit. The link does not show this build's local changes, so it cannot establish what those changes do."
      } else {
        "Check those claims against this app bundle and the exact commit linked above."
      }
    return """
      Intent: help me verify this exact OpenTrawl build before I give it Full Disk Access.

      \(buildDescription)
      \(source)

      This is the app bundle to audit:
      \(appBundlePath)

      OpenTrawl says that this exact \(buildKind):
      - archives data from 11 built-in apps: Messages, WhatsApp, Telegram, Apple Mail, Gmail, Notes, Contacts, Calendar, Photos, Money, and Twitter (X);
      - writes separate search archives under ~/.opentrawl;
      - keeps those archives and my searches on my Mac;
      - has no telemetry or analytics;
      - does not run servers that receive my archive, searches or usage data;
      - keeps app indexing on my Mac by default. The app \(updateClaim). Network requests happen only when I explicitly connect a cloud service, a remote OpenTrawl archive host, or ask OpenTrawl to sync or download something (for example Google for Gmail, Telegram for direct login or missing media downloads, Enable Banking for Money, X for Twitter — including optional user-initiated browser cookie import for X, a remote archive server I configure, or local embedding weights if I enable optional semantic search).

      \(auditTarget) Explain in plain English what OpenTrawl reads, what leaves my Mac, which network requests happen automatically or when I ask for them, whether OpenTrawl receives any of my personal data, and whether the app has telemetry or analytics.

      Keep disabled or feature-flagged pre-release features, tests, debug tools, unfinished work, future code and standalone commands separate from this \(buildKind). Put relevant findings about them in a section called "Not part of this \(buildKind)".

      If you can inspect this app bundle, check that its GitCommit is \(identity.gitCommit)\(identity.hasLocalChanges ? " and note that this local build includes uncommitted changes" : ""). If you cannot inspect this app bundle, continue with the source review and say that the app bundle was not independently checked. Do not treat that fact alone as a privacy problem.

      Finish by telling me whether OpenTrawl's privacy claims are accurate and whether giving this build Full Disk Access is reasonable. Do not access my personal data or change anything.
      """
  }

  static let connectAI = """
    Help me start using OpenTrawl with this AI.

    OpenTrawl is a local archive search tool that is usually installed in /Applications. Its executable is:

    /Applications/OpenTrawl.app/Contents/Helpers/trawl

    Start with a read-only demo:

    1. Run the executable with no arguments for its overview, and with --help to inspect the current interface.
    2. Run a small, read-only search against my archive and open one relevant result to show me how it works.
    3. Explain briefly what worked and how I can ask you to use OpenTrawl in future.
    4. Once you have shown me the demo, ask me what I think and whether I want you to use OpenTrawl more often (for example by adding a short instruction to my AGENTS.md or setting up a skill or MCP connection).

    Use the normal text output.

    Do not run update, install plugins, or change any files or configuration during this demo.

    If I ask you to set up an ongoing integration:

    1. Confirm its scope and target path.
    2. Show me the complete proposed text or exact diff. Keep it as short as possible.
    3. Explain exactly which file would change.
    4. Wait for my explicit approval before writing anything.

    A request to explore an option is not approval to edit a file. Never create or edit AGENTS.md, install a skill, run trawl connect --write, change PATH or modify other configuration without approval of the exact final text.
    """
}
