# prfromissue

Opens pull requests for gh-aw fallback tracking issues and links the two together.

## Why

The porting workflows (`[dotnet-port-api]`, `[dotnet-port-fixes]`) run with a token
that is not permitted to create pull requests. When gh-aw's `create-pull-request`
safe output is blocked, it instead pushes the branch and files a **tracking issue**
containing a compare link. The pull request a human eventually opens from that branch
is not linked back to the issue.

`prfromissue` closes that gap: it scans the target repository's open tracking issues,
opens the pull request from the pushed branch with a `Closes #<issue>` body, and
comments the PR link back on the issue. If a pull request already exists for the
branch, it links the two instead of opening a duplicate.

By default it targets `microsoft/agent-framework-go` and acts on every open issue
opened by `app/github-actions` — the account that files the fallback issues — so an
issue opened by anyone else cannot drive it to open a PR. Issues that do not parse as
a fallback are skipped. Use `-prefix` to narrow to a single workflow's issues.

## Authentication

Uses the `gh` CLI. Authenticate with `gh auth login` locally, or set the
`GH_TOKEN` / `GITHUB_TOKEN` environment variable to a token that can open pull
requests (a PAT or GitHub App token — not the default Actions `GITHUB_TOKEN`) in CI.

## Usage

Install it once and run it from anywhere:

```sh
go install github.com/microsoft/agent-framework-go/cmd/prfromissue@latest

# Open + link PRs for every open fallback tracking issue
prfromissue

# Preview without making any changes
prfromissue -dry-run

# Limit to a single workflow's issues, or open the PRs as drafts
prfromissue -prefix "[dotnet-code]"
prfromissue -draft
```

### Flags

| Flag            | Default                     | Description                                                             |
| --------------- | --------------------------- | ----------------------------------------------------------------------- |
| `-prefix`       | "" (all matching issues)    | Only act on issues whose title starts with this prefix.                 |
| `-repo`         | `microsoft/agent-framework-go` | Target repository as `owner/repo`.                                   |
| `-issue-author` | `app/github-actions`        | Only act on issues opened by this login; empty disables the check.      |
| `-limit`        | 100                         | Maximum number of open issues to scan.                                  |
| `-draft`        | false                       | Open the pull request as a draft.                                       |
| `-dry-run`      | false                       | Print the actions that would be taken without changing anything.        |
