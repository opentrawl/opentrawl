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
  linear comment <ISSUE> --as <actor> [body]
  linear issue new --team <KEY> --title <title> --as <actor> [--description <text>] [--label <name> ...]
  linear issue <ISSUE>
  linear issues --team <KEY> [--state <name>]
  linear mcp

Environment:
  LINEAR_CLIENT_ID      Linear OAuth app client id
  LINEAR_CLIENT_SECRET  Linear OAuth app client secret

Write commands require --as. The value becomes Linear's createAsUser display name.

Examples:
  linear comment TRAWL-99 --as coordinator "Ready for review."
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer --label agent-filed
  linear issue TRAWL-99
  linear issues --team TRAWL
  linear mcp
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

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99

Examples:
  linear issue TRAWL-99
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer
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
  create_comment  Create a comment. Requires issue, actor and body.
  create_issue    Create an issue. Requires team, title and actor.
  get_issue       Show one issue and its comments.
  list_issues     List team issues.
` + commonHelp
