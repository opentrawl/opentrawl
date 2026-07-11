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
  linear issue update <ISSUE> --as <actor> [--description-file <path>] [--priority <priority>] [--project <project>] [--milestone <milestone>] [--title <title>]
  linear issue label add|remove <ISSUE> --label <name> [--label <name> ...] --as <actor>
  linear issue relation add|remove <ISSUE> (--blocks <OTHER> | --blocked-by <OTHER>) --as <actor>
	  linear issue <ISSUE>
	  linear issues --team <KEY> [--project <PROJECT>] [--state <name>]
  linear project <PROJECT>
  linear project update <PROJECT> --as <actor> [--summary <text>] [--description-file <path>] [--status <status>] [--priority <priority>]
  linear project milestone ensure <PROJECT> --name <name> --as <actor> [--description-file <path>]
  linear mcp

Environment:
  LINEAR_CLIENT_ID      Linear OAuth app client id
  LINEAR_CLIENT_SECRET  Linear OAuth app client secret

Issue and comment creation require --as. The value becomes Linear's
createAsUser display name. Issue state and field updates also require --as;
Linear records that actor only in the local request log, and these updates do
not add comments.
Ack writes an eyes reaction as the app user and does not take --as.

Examples:
  linear inbox --team TRAWL
  linear ack 00000000-0000-4000-8000-000000000000
  linear comment TRAWL-99 --as coordinator "Ready for review."
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer --label agent-filed
  linear issue state TRAWL-99 --state Done --as coordinator
  linear issue update TRAWL-99 --as coordinator --priority high --project OpenTrawl
  linear issue label add TRAWL-99 --label agent-filed --as coordinator
  linear issue relation add TRAWL-99 --blocked-by TRAWL-98 --as coordinator
  linear project Photos
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
  linear issue update <ISSUE> --as <actor> [--description-file <path>] [--priority <priority>] [--project <project>] [--milestone <milestone>] [--title <title>]
  linear issue label add|remove <ISSUE> --label <name> [--label <name> ...] --as <actor>
  linear issue relation add|remove <ISSUE> (--blocks <OTHER> | --blocked-by <OTHER>) --as <actor>

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99

Examples:
  linear issue TRAWL-99
  linear issue new --team TRAWL --title "Fix sync output" --as reviewer
  linear issue state TRAWL-99 --state Done --as coordinator
  linear issue update TRAWL-99 --as coordinator --description-file issue.md --priority high
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

const issueUpdateHelp = `Replace selected Linear issue fields.

Usage:
  linear issue update <ISSUE> --as <actor> [--description-file <path>] [--priority <priority>] [--project <project>] [--milestone <milestone>] [--title <title>]

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99

Flags:
  --as <actor>               Required. Actor name recorded in the local request log.
  --description-file <path>  Optional file containing the full replacement description.
                             An empty file clears the description.
  --priority <priority>      Optional. One of none, urgent, high, medium or low.
  --project <project>        Optional project name or slug. Use none to clear it.
  --milestone <milestone>    Optional milestone in the issue's current project. Use none to clear it.
  --title <title>            Optional replacement issue title.

Linear attributes the change to the OpenTrawl app. Issue updates do not
support createAsUser. The --as value records the responsible agent locally
without adding a comment.

Examples:
  linear issue update TRAWL-99 --as coordinator --description-file issue.md
  linear issue update TRAWL-99 --as "lane mac app" --priority high --project OpenTrawl
` + commonHelp

const issueLabelHelp = `Add or remove existing labels on one Linear issue.

Usage:
  linear issue label add <ISSUE> --label <name> [--label <name> ...] --as <actor>
  linear issue label remove <ISSUE> --label <name> [--label <name> ...] --as <actor>

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99.

Flags:
  --label <name>  Required existing label name. Repeat for more labels.
  --as <actor>    Required actor name recorded in the local request log.

The command changes only the named labels, preserves every other label and
reads the issue back before reporting success.

Example:
  linear issue label add TRAWL-99 --label agent-filed --as coordinator
` + commonHelp

const issueRelationHelp = `Add or remove one blocking relation between two Linear issues.

Usage:
  linear issue relation add <ISSUE> (--blocks <OTHER> | --blocked-by <OTHER>) --as <actor>
  linear issue relation remove <ISSUE> (--blocks <OTHER> | --blocked-by <OTHER>) --as <actor>

Arguments:
  ISSUE  Linear issue identifier, for example TRAWL-99.

Flags:
  --blocks <OTHER>      This issue blocks OTHER.
  --blocked-by <OTHER>  OTHER blocks this issue.
  --as <actor>          Required actor name recorded in the local request log.

The command rejects self-relations and reads both issues back before reporting
success. Repeating an add or remove is safe.

Example:
  linear issue relation add TRAWL-99 --blocked-by TRAWL-98 --as coordinator
` + commonHelp

const projectHelp = `Show one Linear project and its milestones.

Usage:
  linear project <PROJECT>
  linear project update <PROJECT> --as <actor> [--summary <text>] [--description-file <path>] [--status <status>] [--priority <priority>]
  linear project milestone ensure <PROJECT> --name <name> --as <actor> [--description-file <path>]

Arguments:
  PROJECT  Project name or slug.

Examples:
  linear project Photos
  linear project update Photos --as lane-photos --status "In Progress" --priority high
  linear project milestone ensure Photos --name "Foundations complete" --as lane-photos
` + commonHelp

const projectUpdateHelp = `Replace selected Linear project fields.

Usage:
  linear project update <PROJECT> --as <actor> [--summary <text>] [--description-file <path>] [--status <status>] [--priority <priority>]

Arguments:
  PROJECT  Project name or slug.

Flags:
  --as <actor>               Required. Actor name recorded in the local request log.
  --summary <text>           Optional replacement summary. Use none to clear it.
  --description-file <path>  Optional file containing the full replacement Markdown description.
                             An empty file clears it.
  --status <status>          Optional current Linear project status name.
  --priority <priority>      Optional. One of none, urgent, high, medium or low.

Linear attributes the change to the OpenTrawl app. Project updates do not
support createAsUser. The --as value records the responsible agent locally
without adding a comment.

Example:
  linear project update Photos --as lane-photos --summary "One clear outcome" --description-file project.md --status "In Progress" --priority high
` + commonHelp

const projectMilestoneHelp = `Manage Linear project milestones.

Usage:
  linear project milestone ensure <PROJECT> --name <name> --as <actor> [--description-file <path>]

Run ` + "`linear project milestone ensure --help`" + ` for details.
` + commonHelp

const projectMilestoneEnsureHelp = `Create or update one Linear project milestone.

Usage:
  linear project milestone ensure <PROJECT> --name <name> --as <actor> [--description-file <path>]

Arguments:
  PROJECT  Project name or slug.

Flags:
  --name <name>              Required milestone name.
  --as <actor>               Required actor name recorded in the local request log.
  --description-file <path>  Optional file containing the full replacement Markdown description.
                             An empty file clears it.

If exactly one milestone has this name, linear updates only the supplied fields.
If none has it, linear creates it. More than one is refused.

Example:
  linear project milestone ensure Photos --name "Foundations complete" --as lane-photos --description-file milestone.md
` + commonHelp

const issuesHelp = `List Linear issues for a team.

Usage:
  linear issues --team <KEY> [--project <PROJECT>] [--state <name>]

Flags:
	--team <KEY>          Required. Linear team key, for example TRAWL.
	--project <PROJECT>   Optional exact project name or slug.
	--state <name>        Optional state name. Without this, linear lists open issues.

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
	  update_issue          Replace selected issue fields. Requires issue and actor.
	  add_issue_labels      Add existing labels to an issue.
	  remove_issue_labels   Remove named labels from an issue.
	  add_issue_relation    Add one blocking relation.
	  remove_issue_relation Remove one blocking relation.
	  list_issues           List team issues.
  get_project                 Show one project and its milestones.
  update_project              Replace selected project fields. Requires project and actor.
  ensure_project_milestone    Create or update one project milestone. Requires project, name and actor.
` + commonHelp
