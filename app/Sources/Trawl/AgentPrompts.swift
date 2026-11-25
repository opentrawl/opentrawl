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

  // Keep aligned with trawl/internal/cli/skill/personal-wiki/SKILL.md.
  static let personalWiki = """
    # Build my personal wiki from OpenTrawl

    Every agent I use starts each task knowing nothing about me. It doesn't know who
    my partner is, where I live, what I do for work, which friend a first name in
    my messages means, or which of my matters are still open. OpenTrawl holds my
    digital history on this machine, but searching it from cold for every task is
    slow, and an agent that doesn't know what to search for never finds it.

    Build me a personal wiki: a set of Markdown pages that tells any agent who I am
    and how my life fits together, and points to the records that prove it. An
    agent should be able to read the index in under a minute and then do useful
    work for me without asking questions you could have answered from my archive.

    ## First, ask me one question

    Before you start, ask me once how much sensitive detail I want in the wiki:

    - my health: conditions, treatment and medication, or only my providers;
    - my money: amounts such as salary, settlements and debts, or only my banks,
      accounts and providers;
    - other people: their own health, work and money, or only how they relate to me
      and how to reach them.

    If I don't answer, or don't say, use the default: providers, accounts and
    contacts go in; the sensitive details stay out. Then work through to the end
    without stopping again.

    ## What good looks like

    - **Grounded.** Every fact comes from a record you opened and carries its
      OpenTrawl link, copied exactly as OpenTrawl returned it, so a reader can open
      it (`trawl_open` or `trawl open LINK`) and check it. If you only saw a search
      snippet, open the record before you rely on it. When records disagree, say so
      and cite both. When you infer something rather than read it, say that it is
      an inference. When a record reports what someone said, say who said it. A
      wrong fact is worse than a missing one.
    - **Dated.** The wiki will be read months from now. Give every status the date
      of the record that shows it, and don't present a plan, booking or draft as
      something that happened.
    - **Durable.** Write what will still be true next month: people and how they
      relate to me, homes, work history, regular places and services, travel,
      interests, health and admin providers, long-running matters, and the
      identifiers an agent needs to act. Leave out what changes daily, such as
      balances or today's to-dos.
    - **Complete enough to act on.** For someone with years of history, a useful
      wiki usually holds several hundred cited facts. Cover every page in the map
      below before you polish any one of them.
    - **Readable.** Plain, specific English. Tables or lists where they help.
    - **Safe.** The wiki stays private to the user. Never copy passwords, one-time
      codes, recovery keys or session tokens into it, whatever I answered above.

    ## Suggested page map

    Start from this map and adapt it to my archive: drop a page it has nothing for,
    split a page that grows past a few screens, and add a page for anything big
    that doesn't fit, such as a long legal matter, a business or a trip log.

    | Page | What it holds |
    | --- | --- |
    | `index.md` | Who I am in a few lines: partner and household, where I live, what I do now. The few identifiers agents need most. The traps a cold agent must know. A map of the other pages. |
    | `people.md` | Partner, family, close friends, colleagues and professionals: how each relates to me, how to reach them, which chat to read, and names that are easy to mix up. |
    | `work.md` | Current work and projects, past employers and roles with dates, education, public accounts and handles. |
    | `homes-and-admin.md` | Homes, utilities, banks, insurers, health providers, vehicles, pets and government accounts, with the reference numbers needed to act. |
    | `open-matters.md` | Each long-running matter: its last dated status, who is involved, and the latest record to read. |
    | `travel-and-interests.md` | Past and planned trips, hobbies and tastes, each with evidence. |
    | `search-guide.md` | Which source holds what and how fresh it is; aliases, duplicate contacts and other-language search words; what is missing or broken. |

    ## Good and bad entries

    The angle brackets stand for real values from my archive.

    Good, because the status is dated, sourced and specific:

    > **Sale of <old flat>:** unsold on <date> (`gmail:<link>`). Agent <name> at
    > <agency> reported <n> viewings since the price cut. Service charge still
    > paid on <date> (`money:<link>`).

    Bad, because there is no date and no source, and it may already be false:

    > The old flat is still for sale.

    Good, because it says who said it:

    > On <date> I told <friend> that <company> stopped replying after its offer
    > (`telegram:<link>`).

    Bad, because it turns one person's account into a fact, and the source is a
    search snippet:

    > <Company> withdrew its offer.

    Good, because it is compact, and the inference is labelled and sourced:

    > | <Name> | Brother (inferred from Dad's messages, `imessage:<link>`) | Lives
    > in <city> (`contacts:<link>`) | Telegram `@<handle>` |

    Bad, because it is padded, vague and uncited:

    > | <Name> | Very important family member | They are close and talk often about
    > many topics. |

    ## How to work

    Start with `trawl_status` (or `trawl status` and `trawl --help` when using the
    CLI; if `trawl` is not on your PATH, use
    `/Applications/OpenTrawl.app/Contents/Helpers/trawl`) to see what is archived
    and how fresh it is. Use `trawl_search`, `trawl_open`, `trawl_around`, and
    `trawl_who` over MCP — or the matching `trawl` CLI commands locally — to read
    my archive the way a new chief of staff would on their first day: find the
    people I talk to most, read enough of those conversations to understand each
    relationship, and follow threads across mail, messages, calendar, notes, money
    and photos. Search works best with concrete words, so try names, places and
    alternative spellings. Use your own judgement about what matters; don't write
    scripts to classify my records for you.

    I'm waiting for this, so aim for a strong first version within about an hour
    rather than an exhaustive one tomorrow. The wiki can be refreshed later.

    If the `trawl` CLI gets in your way locally (you can't find something that
    should be there, a command fails, or the output is confusing), record it with
    `trawl complain "what went wrong" --about "the command" --trying "what you wanted"`
    and carry on.

    ## When you finish

    Write the wiki to `~/.opentrawl/wiki/` unless I named another folder or asked
    for it in chat. Then tell me in a few lines what the wiki covers, what you
    could not find or check, and which page to read first.
    """
}
