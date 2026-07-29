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
    let buildDescription =
      if identity.hasLocalChanges {
        "This local build is based on Git commit \(identity.gitCommit) and includes uncommitted changes. The link shows the base code:"
      } else {
        "This is OpenTrawl \(identity.version), built from Git commit \(identity.gitCommit):"
      }
    let auditTarget =
      if identity.hasLocalChanges {
        "Check those claims against the production Mac app and the base commit above. The link does not show this build's local changes."
      } else {
        "Check those claims against the production Mac app at the exact commit above."
      }
    return """
      Intent: help me verify this exact OpenTrawl build before I give it Full Disk Access.

      \(buildDescription)
      \(source)

      OpenTrawl says that its production beta:
      - reads local data from Messages, WhatsApp, Telegram, Notes and Contacts;
      - writes separate search archives under ~/.opentrawl;
      - keeps those archives and my searches on my Mac;
      - has no telemetry or analytics;
      - does not run servers that receive my archive, searches or usage data;
      - keeps app indexing on my Mac by default. The app checks GitHub for updates. If I ask OpenTrawl to download missing Telegram media, it requests that media from Telegram.

      \(auditTarget) Explain in plain English what OpenTrawl reads, what leaves my Mac, which network requests happen automatically or when I ask for them, whether OpenTrawl receives any of my personal data, and whether the app has telemetry or analytics.

      Keep disabled or feature-flagged pre-release features, tests, debug tools, unfinished work, future code and standalone commands separate from the production beta. Put relevant findings about them in a section called "Not part of the production beta".

      If you can inspect the installed app, check that its GitCommit is \(identity.gitCommit)\(identity.hasLocalChanges ? " and note that this local build includes uncommitted changes" : ""). If you cannot check the installed app, continue with the source review and say that the installed build was not independently checked. Do not treat that fact alone as a privacy problem.

      Finish by telling me whether OpenTrawl's privacy claims are accurate and whether giving this build Full Disk Access is reasonable. Do not access my personal data or change anything.
      """
  }

  static let connectAI = """
    Help me start using OpenTrawl in this coding-agent harness.

    OpenTrawl is a local archive search tool. Its executable is:

    /Applications/OpenTrawl.app/Contents/Helpers/trawl

    Start with session-only use:

    1. Run the executable with no arguments for its introduction.
    2. Run it with --help to inspect the current interface.
    3. Perform a small, read-only smoke test: search the archive and open one relevant result.
    4. Optionally do limited additional exploration to orient yourself with the tool as needed.
    5. Explain briefly what worked and how I can ask you to use OpenTrawl in future.

    Use the normal text output.

    Do not change any files or configuration during this process.

    After the smoke test, you may offer these optional integrations:

    - Add a short OpenTrawl instruction to AGENTS.md.
    - Create a small OpenTrawl skill.

    Only discuss or draft an integration if I explicitly ask for it.

    If I ask for one:

    1. Confirm its scope and target path.
    2. Show me the complete proposed text. Keep it as short as possible.
    3. Explain exactly which file would change.
    4. Wait for my explicit approval before writing anything.

    A request to explore an option is not approval to edit a file. Never create or edit AGENTS.md, install a skill, change PATH or modify other configuration without approval of the exact final text.
    """
}
