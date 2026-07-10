package main

const issueCoreFields = `
      id
      identifier
      title
      description
      url
      priorityLabel
      state { name type }
      project { id name slugId }
      projectMilestone { id name }
      assignee { displayName name }
      labels(first: 50) { nodes { id name } pageInfo { hasNextPage } }`

const commentFields = `
        id
        url
        createdAt
        body
        user { id displayName name }
        botActor { name userDisplayName type subType }
        externalUser { displayName name }`

const inboxCommentFields = commentFields + `
        reactions { emoji user { id } }
        issue { identifier title }`

const issueDetailFields = issueCoreFields + `
      comments(first: 100, after: $commentsAfter, orderBy: createdAt) {
        nodes {` + commentFields + `
        }
        pageInfo { hasNextPage endCursor }
      }`

const issueListFields = `
      id
      identifier
      title
      state { name type }`

const resolveIssueIDQuery = `
query ResolveIssueID($team: String!, $number: Float!) {
  issues(first: 2, filter: {team: {key: {eq: $team}}, number: {eq: $number}}) {
    nodes {
      id
      identifier
      project { id name slugId }
    }
  }
}`

const issueByIdentifierQuery = `
query IssueByIdentifier($team: String!, $number: Float!, $commentsAfter: String) {
  issues(first: 2, filter: {team: {key: {eq: $team}}, number: {eq: $number}}) {
    nodes {` + issueDetailFields + `
    }
  }
}`

const listIssuesQuery = `
query ListIssues($filter: IssueFilter!) {
  issues(first: 50, filter: $filter) {
    nodes {` + issueListFields + `
    }
    pageInfo { hasNextPage }
  }
}`

const viewerIDQuery = `
query ViewerID {
  viewer { id }
}`

const inboxCommentsQuery = `
query InboxComments($filter: CommentFilter, $after: String) {
  comments(first: 100, after: $after, filter: $filter, orderBy: createdAt) {
    nodes {` + inboxCommentFields + `
    }
    pageInfo { hasNextPage endCursor }
  }
}`

const teamStatesQuery = `
query TeamStates($team: String!) {
  workflowStates(first: 100, filter: {team: {key: {eq: $team}}}) {
    nodes { id name type }
    pageInfo { hasNextPage }
  }
}`

const resolveTeamQuery = `
query ResolveTeam($key: String!) {
  teams(first: 2, filter: {key: {eq: $key}}) {
    nodes { id key name }
  }
}`

const resolveProjectQuery = `
query ResolveProject($reference: String!) {
  projects(first: 10, filter: {or: [
    {name: {eq: $reference}},
    {slugId: {eq: $reference}}
  ]}) {
    nodes { id name slugId }
    pageInfo { hasNextPage }
  }
}`

const projectCoreFields = `
      id
      name
      slugId
      description
      content
      status { id name }
      priority
      priorityLabel
      health
      lead { displayName name }`

const projectByIDQuery = `
query ProjectByID($id: String!, $milestonesAfter: String, $issuesAfter: String, $readMilestones: Boolean!, $readIssues: Boolean!) {
  project(id: $id) {` + projectCoreFields + `
    projectMilestones(first: 100, after: $milestonesAfter) @include(if: $readMilestones) {
      nodes { id name description project { id name slugId } }
      pageInfo { hasNextPage endCursor }
    }
    issues(first: 100, after: $issuesAfter) @include(if: $readIssues) {
      nodes { id state { type } }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const projectStatusesQuery = `
query ProjectStatuses($after: String) {
  projectStatuses(first: 100, after: $after) {
    nodes { id name }
    pageInfo { hasNextPage endCursor }
  }
}`

const resolveLabelsQuery = `
query ResolveLabels($names: [String!]!) {
  issueLabels(first: 100, filter: {name: {in: $names}}) {
    nodes {
      id
      name
      isGroup
      team { id key name }
    }
    pageInfo { hasNextPage }
  }
}`

const createCommentMutation = `
mutation CreateComment($input: CommentCreateInput!) {
  commentCreate(input: $input) {
    success
    comment {` + commentFields + `
    }
  }
}`

const updateIssueMutation = `
mutation UpdateIssue($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) {
    success
    issue {` + issueCoreFields + `
    }
  }
}`

const updateProjectMutation = `
mutation UpdateProject($id: String!, $input: ProjectUpdateInput!) {
  projectUpdate(id: $id, input: $input) {
    success
  }
}`

const createProjectMilestoneMutation = `
mutation CreateProjectMilestone($input: ProjectMilestoneCreateInput!) {
  projectMilestoneCreate(input: $input) {
    success
  }
}`

const updateProjectMilestoneMutation = `
mutation UpdateProjectMilestone($id: String!, $input: ProjectMilestoneUpdateInput!) {
  projectMilestoneUpdate(id: $id, input: $input) {
    success
  }
}`

const createIssueMutation = `
mutation CreateIssue($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue {` + issueCoreFields + `
    }
  }
}`

const ackLookupQuery = `
query AckLookup($id: String!) {
  comment(id: $id) {
    id
    issue { identifier }
    reactions { user { id } }
  }
}`

const ackCommentMutation = `
mutation AckComment($input: ReactionCreateInput!) {
  reactionCreate(input: $input) {
    success
    reaction {
      id
      emoji
      comment {
        id
        issue { identifier }
      }
    }
  }
}`
