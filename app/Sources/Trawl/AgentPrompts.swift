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

  static func connectAI(helperCommand: String) -> String {
    """
    Intent: connect my AI to the local OpenTrawl archive without changing configuration until I approve it.

    Use \(helperCommand) to search and open my local OpenTrawl archives. Run it with no arguments for a short introduction and with --help for the complete current interface. Prefer normal text output. Use --json only when writing a script. Do not install a skill, change PATH or edit configuration without showing me the exact change and asking for approval first.
    """
  }
}
