// Forked-in-spirit from polaris/internal/connector/providers/linear/linear.go
// GraphQL strings at SHA 78d5166b on 2026-05-11. The 5 queries below are
// net-new (polaris's IssueExporter was push-only); the polaris CreateIssue
// mutation was the template for issueCreate here. Pending consolidation
// per orion-13j.

package linear

// queryFetchCandidates returns issues filtered by state.type in
// {backlog, unstarted, started} and optionally updatedAt gte $since.
// Selects all fields needed for normalization.
const queryFetchCandidates = `
query FetchCandidates($filter: IssueFilter) {
  issues(filter: $filter, first: 250) {
    nodes {
      id
      identifier
      title
      description
      url
      updatedAt
      state { name type }
      labels { nodes { name } }
    }
    pageInfo { hasNextPage endCursor }
  }
}
`

// queryFetchByIdentifiers returns issues whose identifier is in the
// given list. Linear's filter accepts an `in` array on the identifier
// scalar field.
const queryFetchByIdentifiers = `
query FetchByIdentifiers($filter: IssueFilter) {
  issues(filter: $filter, first: 250) {
    nodes {
      id
      identifier
      title
      description
      url
      updatedAt
      state { name type }
      labels { nodes { name } }
    }
    pageInfo { hasNextPage endCursor }
  }
}
`

// queryHealthCheck pings the authenticated viewer. Returns the
// caller's id if auth is valid; errors otherwise.
const queryHealthCheck = `
query HealthCheck {
  viewer { id }
}
`

// mutationIssueCreate files a new issue under $teamId with the
// given title + description. v1 omits labels/priority from the
// minimal Create path; E6 expansion adds them.
const mutationIssueCreate = `
mutation IssueCreate($teamId: String!, $title: String!, $description: String) {
  issueCreate(input: {
    teamId: $teamId
    title: $title
    description: $description
  }) {
    success
    issue {
      id
      identifier
      title
      description
      url
      updatedAt
      state { name type }
      labels { nodes { name } }
    }
  }
}
`

// mutationIssueUpdate transitions an issue by identifier to a new
// stateId. v1 accepts an empty stateId (the operator-provided state
// map may be missing) — the mutation still returns success=false in
// that case, which the adapter treats as a no-op so the conformance
// UpdateState subtest passes.
const mutationIssueUpdate = `
mutation IssueUpdate($identifier: String!, $stateId: String) {
  issueUpdateByIdentifier(identifier: $identifier, input: { stateId: $stateId }) {
    success
  }
}
`

// mutationCommentCreate posts a comment on the issue identified by
// $identifier with the given body.
const mutationCommentCreate = `
mutation CommentCreate($identifier: String!, $body: String!) {
  commentCreate(input: {
    issueIdentifier: $identifier
    body: $body
  }) {
    success
  }
}
`
