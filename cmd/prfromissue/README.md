# prfromissue

Opens pull requests for gh-aw fallback tracking issues and links the two together.

## Why

The porting workflows (`[dotnet-port-api]`, `[dotnet-port-fixes]`) run with a token
that is not permitted to create pull requests. When gh-aw's `create-pull-request`
safe output is blocked, it instead pushes the branch and files a **tracking issue**
containing a compare link. The pull request a human eventually opens from that branch
is not linked back to the issue.

`prfromissue` closes that gap: it scans open issues by title prefix, opens the pull
request from the pushed branch with a `Closes #<issue>` body, and comments the PR link
back on the issue. If a pull request already exists for the branch, it links the two
instead of opening a duplicate.

## Authentication

Uses the `gh` CLI. Authenticate with `gh auth login` locally, or set the
`GH_TOKEN` / `GITHUB_TOKEN` environment variable to a token that can open pull
requests (a PAT or GitHub App token — not the default Actions `GITHUB_TOKEN`) in CI.

## Usage

```sh
# Open + link PRs for every open [dotnet-port-api] tracking issue in the current repo
go run ./cmd/prfromissue -prefix "[dotnet-port-api]"

# Preview without making any changes
go run ./cmd/prfromissue -prefix "[dotnet-port-fixes]" -dry-run

# Target a specific repo and open non-draft PRs
go run ./cmd/prfromissue -prefix "[dotnet-port-api]" -repo microsoft/agent-framework-go -draft=false
```

### Flags

| Flag       | Default | Description                                                        |
| ---------- | ------- | ------------------------------------------------------------------ |
| `-prefix`  | —       | Required. Only act on issues whose title starts with this prefix.  |
| `-repo`    | ""      | `owner/repo`; defaults to the repository in the current directory. |
| `-limit`   | 100     | Maximum number of open issues to scan.                             |
| `-draft`   | true    | Open the pull request as a draft.                                  |
| `-dry-run` | false   | Print the actions that would be taken without changing anything.   |

## CI

The same binary runs in a workflow. Provide a PAT with pull-request permissions via
`GH_TOKEN`, then invoke it on a schedule or `workflow_dispatch`:

```yaml
- run: go run ./cmd/prfromissue -prefix "[dotnet-port-api]"
  env:
    GH_TOKEN: ${{ secrets.GH_AW_GITHUB_TOKEN }}
```
