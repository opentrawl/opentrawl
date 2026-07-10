---
written_by: ai
---

# Linear CLI

`linear` posts to Linear as an OAuth app actor. Write commands require
`--as`, so every agent-created issue or comment carries an explicit
display name instead of posting as the human OAuth user. Linear state
and field changes carry no display name, so `issue state` and `issue
update` record the actor in the request log instead. They do not add a
comment.

## Build

Run this from `scripts/linear`:

```sh
go build -o linear .
```

The repo MCP config runs `scripts/linear/linear mcp`, so rebuild the
binary after changing this module.

## Configure

Set the OAuth app credentials in the environment:

```sh
export LINEAR_CLIENT_ID=...
export LINEAR_CLIENT_SECRET=...
```

The CLI caches the app token at `~/.opentrawl/linear/token.json` with
file mode `0600`.

## Use

```sh
linear inbox --team TRAWL
linear ack 00000000-0000-4000-8000-000000000000
linear comment TRAWL-99 --as coordinator "Ready for review."
linear issue new --team TRAWL --title "Fix sync output" --as reviewer --label agent-filed
linear issue state TRAWL-99 --state Done --as coordinator
linear issue update TRAWL-99 --as coordinator --description-file issue.md --priority high
linear issue update TRAWL-99 --as coordinator --project OpenTrawl
linear issue update TRAWL-99 --as coordinator --milestone "Foundations complete"
linear issue update TRAWL-99 --as coordinator --title "Clarify the project wrapper"
linear issue TRAWL-99
linear issues --team TRAWL
linear project OpenTrawl
linear project update OpenTrawl --as coordinator --summary "One clear outcome" --description-file project.md --status "In Progress" --priority high
linear project milestone ensure OpenTrawl --name "Foundations complete" --as coordinator --description-file milestone.md
linear mcp
```

`linear issue` shows the full description, priority, project, milestone and
assignee. `issue update` replaces only the fields named on the command.
Use `--project none` or `--priority none` to clear those fields. An
empty description file clears the description. Use `--milestone none` to clear
an issue milestone.

`linear project` reads the full Markdown brief, current status and priority,
read-only health, lead, milestones and issue totals. It reads every milestone
and issue page. `project update` replaces only the named summary, Markdown
brief, status or priority fields and then reads the project back. `--summary
none` clears the summary. `project milestone ensure` creates a named milestone
when absent, or updates only the supplied fields when exactly one exists. It
refuses duplicate names. All project and issue field writes require `--as`, use
the OpenTrawl OAuth app and add no Linear comment.

## Directive queue

Josh leaves human-authored comments on Linear issues. Agents run
`linear inbox` to find comments from Josh that the app user has not
reacted to yet.

Convention is ack-then-act: run `linear ack <COMMENT-ID>` first, then
act on the directive. If the tool says `already acked`, another agent
owns that directive, so skip it.

The ack is an eyes reaction from the Linear app user. That reaction is
the queue state. There is no local inbox file.

The ack is not an atomic claim. If 2 agents ack within the same instant,
both can see `acked`: Linear treats duplicate reactions as success. The
tool pre-checks the comment's reactions, which catches every normal
case but not a simultaneous race. We accept that tradeoff.

Claimed-but-crashed work is visible to Josh: an eyes reaction with no
follow-up comment means re-ping.
