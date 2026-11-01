# Agents

Agents archives the conversations you hold with AI agents in Claude Code,
Codex and T3 Code, so you and your agents can find what was said in any of
them. It reads each harness's own store on this Mac. Nothing leaves the Mac
unless you name a collecting Mac (below).

`trawl search` includes every turn: yours, the agents' replies, and messages
that another agent or a program wrote into a conversation. `trawl open` shows a
turn whole, with the turns around it, and a link that opens the conversation in
its own app where the app has one: `t3-thread://` for T3 Code and
`codex://threads/` for the Codex app. Claude Code has no such link.

`trawl agents messages --from-me` lists only the messages you sent, newest
first. Add `--matching WORDS` to find your own words at the passage that
matched, `--after` and `--before` for a period, and `--conversation LINK` to
stay in one conversation. Words in double quotes match as a phrase. `trawl agents conversations` lists conversations by
their latest turn.

## Who wrote a turn

Each turn is marked `me`, the agent (`Claude`, `Codex`), or `agent or program`.
A turn is yours only when the store shows a person sent it. No store tells
text you typed from text you pasted, such as another agent's brief you pasted
into a new session; both are yours. Open a turn to read what came before it.

- **T3 Code** records who created each message and where. A message is
  yours when a person created it in a T3 Code window or on the phone; T3 Code's
  own task notices, scheduled prompts and resumes are a program's.
- **Claude Code** records messages from another session as a peer, and
  sessions started with `claude -p` as a program's.
- **Codex** does not tell a person from a program in the app. A user turn is
  yours when the thread was started by a person in the Codex app and the text
  does not start with one of Codex's markup tags, such as `<heartbeat>`.
  Threads from `codex exec` and sub-agents are marked `agent or program`.

Sessions that T3 Code drove are read from T3 Code, which knows who wrote each
message, and not again from Claude Code or Codex. Codex's approval-review
threads are left out: nobody holds a conversation in them.

## What it keeps

The archive only grows. A conversation that a harness deletes, or a Codex
rollout moved off this Mac, keeps every turn the archive already read.
Claude Code copies earlier turns into a resumed session; each turn belongs to
the session where it was first written.

A comment on a quoted part of an agent's reply, which T3 Code sends as
a link, read as the comment followed by `Replying to:` and the quote.

Agents keeps turn text and the names of attached files. Tool calls and their
output stay in the harness's own store.

## More than one Mac

One Mac collects every Mac's conversations into one archive and gives each
record its link, so a link opens the same turn on every Mac and one search
covers all of them. Choose the Mac that is always on. On each other Mac, name
it in `config.toml`:

```toml
collecting_machine_ssh_host = "mac-mini"
```

`trawl update agents` on that Mac then reads its own stores, sends what
changed to the collecting Mac over SSH, has it update, and copies its archive
back with rsync. While that Mac is shut, the collecting Mac still holds its
conversations up to its last update. If the collecting Mac cannot be reached,
the update fails and says so; the next update sends the same conversations.
`collecting_machine_trawl_location` says where `trawl` is on the collecting
Mac; the default is the OpenTrawl app's copy.

## Configuration

`config.toml` in the Agents archive folder can also set `machine_name`,
`claude_code_projects_folder`, `codex_home_folder` and
`t3_code_user_data_folder`. The defaults are this Mac's name and the usual
folders in your home folder.
