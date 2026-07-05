package main

const issueCoreFields = `
      id
      identifier
      title
      url
      state { name type }
      assignee { displayName name }
      labels(first: 50) { nodes { id name } pageInfo { hasNextPage } }`

const commentFields = `
        id
        url
        createdAt
        body
        user { displayName name }
        botActor { name userDisplayName type subType }
        externalUser { displayName name }`

const issueDetailFields = issueCoreFields + `
      comments(first: 100) {
        nodes {` + commentFields + `
        }
        pageInfo { hasNextPage }
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
    }
  }
}`

const issueByIdentifierQuery = `
query IssueByIdentifier($team: String!, $number: Float!) {
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

const teamStatesQuery = `
query TeamStates($team: String!) {
  workflowStates(first: 100, filter: {team: {key: {eq: $team}}}) {
    nodes { name type }
    pageInfo { hasNextPage }
  }
}`

const resolveTeamQuery = `
query ResolveTeam($key: String!) {
  teams(first: 2, filter: {key: {eq: $key}}) {
    nodes { id key name }
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

const createIssueMutation = `
mutation CreateIssue($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue {` + issueCoreFields + `
    }
  }
}`
