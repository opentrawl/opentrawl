package main

const commonHelp = `
Global flags:
  -v   Write request summaries to stderr.
  -vv  Write request and response bodies to stderr.

Log:
  linear appends request logs to ~/.opentrawl/linear/linear.log.
`

const rootHelp = `linear posts to Linear as an OAuth app actor.

Usage:
  linear inbox [--team <KEY>] [--since <duration>] [--all]
  linear ack <COMMENT-ID>
  linear comment <ISSUE> --as <actor> [body]
  linear issue new --team <KEY> --title <title> --as <actor> [--description <text>] [--label <name> ...]
  linear issue state <ISSUE> --state <name> --as <actor>
  linear issue <ISSUE>
  linear issues --team <KEY> [--state <name>]
  linear mcp

Environment:
  LINEAR_CLIENT_ID      Linear OAuth app client id
  LINEAR_CLIENT_SECRET  Linear OAuth app client secret

Issue and comment write commands require --as. The value becomes Linear's createAsUser display name.
Ack writes an eyes reaction as the app user and does not take --as.

Examples:
  linear inbox --team TRAWL
  linear ack 00000000-0000-4000-8000-000000000000
  linear comment TRAWL-99 --as coordinator "Ready for review."
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer --label agent-filed
  linear issue state TRAWL-99 --state Done --as coordinator
  linear issue TRAWL-99
  linear issues --team TRAWL
  linear mcp
` + commonHelp

const inboxHelp = `List Josh's unacknowledged human comments.

Usage:
  linear inbox [--team <KEY>] [--since <duration>] [--all]

Flags:
  --team <KEY>          Optional Linear team key, for example TRAWL.
  --since <duration>    Optional window. Uses Go duration syntax plus d for days. Default: 14d.
  --all                 List across all time. Cannot be used with --since.

Examples:
  linear inbox --team TRAWL
  linear inbox --team TRAWL --since 36h
  linear inbox --all --team TRAWL
` + commonHelp

const ackHelp = `Mark a Linear comment as picked up.

Usage:
  linear ack <COMMENT-ID>

Arguments:
  COMMENT-ID  Linear comment id.

Ack writes an eyes reaction as the app user. Reactions cannot carry a createAsUser display name, so this is fleet-level and does not take --as.

Example:
  linear ack 00000000-0000-4000-8000-000000000000
` + commonHelp

const commentHelp = `Create a Linear comment as an app actor display name.

Usage:
  linear comment <ISSUE> --as <actor> [body]

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99
  body   Comment body. If omitted, linear reads stdin.

Flags:
  --as <actor>  Required. Display name for Linear createAsUser.

Example:
  linear comment TRAWL-99 --as coordinator "Ready for review."
` + commonHelp

const issueHelp = `Show one Linear issue and its comments.

Usage:
  linear issue <ISSUE>
  linear issue new --team <KEY> --title <title> --as <actor> [--description <text>] [--label <name> ...]
  linear issue state <ISSUE> --state <name> --as <actor>

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99

Examples:
  linear issue TRAWL-99
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer
  linear issue state TRAWL-99 --state Done --as coordinator
` + commonHelp

const issueNewHelp = `Create a Linear issue as an app actor display name.

Usage:
  linear issue new --team <KEY> --title <title> --as <actor> [--description <text>] [--label <name> ...]

Flags:
  --team <KEY>          Required. Linear team key, for example TRAWL.
  --title <title>       Required. Issue title.
  --as <actor>          Required. Display name for Linear createAsUser.
  --description <text>  Optional issue description.
  --label <name>        Optional label name. Repeat for more labels.

Example:
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer --label agent-filed
` + commonHelp

const issueStateHelp = `Move a Linear issue to a workflow state.

Usage:
  linear issue state <ISSUE> --state <name> --as <actor>

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99

Flags:
  --state <name>  Required. Workflow state name, for example "In Progress".
                  An unknown name is refused with the team's valid states.
  --as <actor>    Required. Linear state changes carry no display name, so
                  the actor is recorded in the request log.

Example:
  linear issue state TRAWL-99 --state Done --as coordinator
` + commonHelp

const issuesHelp = `List Linear issues for a team.

Usage:
  linear issues --team <KEY> [--state <name>]

Flags:
  --team <KEY>    Required. Linear team key, for example TRAWL.
  --state <name>  Optional state name. Without this, linear lists open issues.

Example:
  linear issues --team TRAWL --state "In Progress"
` + commonHelp

const mcpHelp = `Run the Linear MCP server over stdio.

Usage:
  linear mcp

Tools:
  inbox          List Josh's unacknowledged directive comments.
  ack_comment    Mark a directive comment as picked up with an eyes reaction.
  create_comment  Create a comment. Requires issue, actor and body.
  create_issue    Create an issue. Requires team, title and actor.
  get_issue       Show one issue and its comments.
  list_issues     List team issues.
` + commonHelp
