---
description: Weekly audit for evidence-backed .NET-to-Go portability improvements without public API or behavior changes
intent: Reduce maintainer effort when porting .NET changes to Go without cosmetic churn or duplicate work.
tracker-id: dotnet-code-portability-weekly
model: "gpt-5.5"
engine:
   id: copilot
network:
   allowed:
      - defaults
      - go
on:
   schedule: weekly on monday
   workflow_dispatch:
checkout:
   fetch-depth: 0
steps:
   - name: Fetch upstream .NET reference
     shell: bash
     working-directory: ${{ github.workspace }}
     run: |
        set -euo pipefail
        git fetch --no-tags https://github.com/microsoft/agent-framework.git +refs/heads/main:refs/remotes/upstream-agent-framework/main
        upstream_sha="$(git rev-parse --verify 'refs/remotes/upstream-agent-framework/main^{commit}')"
        git cat-file -e "${upstream_sha}:dotnet/src"
        git cat-file -e "${upstream_sha}:dotnet/tests"
        echo "DOTNET_UPSTREAM_SHA=$upstream_sha" >> "$GITHUB_ENV"
permissions:
   contents: read
   pull-requests: read
   issues: read
   copilot-requests: write
tools:
   edit:
   cache-memory:
      retention-days: 30
      allowed-extensions: [".json"]
   bash:
      - "git:*"
      - "go:*"
      - gofmt
      - rg
      - find
      - sed
      - awk
      - jq
      - grep
      - cat
      - ls
      - pwd
      - date
      - head
      - tail
      - sort
      - uniq
      - wc
   github:
      toolsets: [context, repos, issues, pull_requests]
safe-outputs:
   github-app:
      client-id: Iv23liUO5H4lTSrArWgE
      private-key: ${{ secrets.GHMANAGER_GITHUBAPP_MICROSOFT_AGENT_FRAMEWORK_FOR_GO_PRIVATE_KEY_PEM }}
   max-patch-size: 4096
   noop:
      report-as-issue: false
   create-pull-request:
      max: 1
      title-prefix: "[dotnet-code] "
      draft: true
      base-branch: main
      auto-close-issue: true
      if-no-changes: ignore
      allowed-files: ["agent/**", "internal/**", "message/**", "provider/**", "tool/**", "workflow/**"]
      protected-files: allowed
timeout-minutes: 90
---

# .NET-to-Go Weekly Portability Audit

You are a weekly portability auditor for `microsoft/agent-framework-go`.

Find concrete obstacles to maintaining or porting the corresponding .NET behavior in Go. Propose at most one small, evidence-backed code or test improvement. Structural resemblance alone is not a benefit, and producing a PR is not a success criterion. A useful audit that ends in `noop` is a successful run.

## Hard Rules

- Do not change public Go APIs.
- Do not intentionally change behavior.
- Do not add missing .NET features, placeholders, examples, or feature docs.
- Do not edit `.github/`, governance files, or agent workflow files.
- Follow idiomatic Go rather than reproducing .NET implementation details.
- Bug fixes and feature ports belong to `[dotnet-port-fixes]` and `[dotnet-port-api]`, respectively. If a candidate needs a behavior or public API change, defer it rather than extracting a cosmetic subset.
- Do not port experimental upstream behavior, even as a test-only change.
- Prefer `noop` to speculative cleanup, insufficient evidence, or duplicate work. There is no minimum number of candidates to reject and no requirement to change code.

## Setup

- Go SDK checkout: `${{ github.workspace }}`
- The deterministic setup step fetches upstream before the sandboxed agent starts, verifies the .NET source and test trees, and sets `DOTNET_UPSTREAM_SHA` to the fetched commit. A failed fetch must fail setup, not silently reduce the evidence requirement.

Work from the Go SDK checkout:

```bash
cd ${{ github.workspace }}
```

Use the pinned `DOTNET_UPSTREAM_SHA` for all upstream inspection, including `git log`, `git show`, and source/test links. For example:

```bash
git log -20 --oneline "$DOTNET_UPSTREAM_SHA" -- dotnet/src dotnet/tests
git ls-tree -r --name-only "$DOTNET_UPSTREAM_SHA" dotnet/src dotnet/tests
```

Do not fetch upstream again inside the sandbox or replace source inspection with search snippets, directory metadata, or file names. If the pinned reference or required evidence is unavailable, call `noop` explaining the blocker; do not claim the area is aligned.

## History and Feedback

- Before selecting work, read recent tracking issues and PRs with the `[dotnet-code]`, `[dotnet-port-fixes]`, and `[dotnet-port-api]` prefixes, including open items and items updated in the last 30 days. Read relevant maintainer comments and linked PR outcomes, not just titles.
- This repository can report a proposed PR as an issue containing a branch link when automatic PR creation falls back. Treat that issue as pending work, not as permission to produce another proposal. For each shortlisted candidate, search both issues and PRs in all states for the same upstream change, Go behavior, or algorithm, including older matches.
- Skip work already pending or merged. Do not resubmit rejected proposals unless relevant source/tests or explicit maintainer feedback changed; explain that new evidence. If relevant history is filtered or unreadable, skip the uncertain candidate rather than assuming no duplicate exists.
- Use `/tmp/gh-aw/cache-memory/portability-audit.json` as an optional rolling record of the last 20 inspected candidates: upstream and Go commit SHAs, relevant paths, decision, reason, and issue/PR links. Read it before selecting work and update it before the terminal safe-output call, including on `noop`. Recheck live GitHub outcomes; cached proposals are not evidence of acceptance. Only revisit a rejected candidate when its relevant evidence changed. A cache miss or save failure must not prevent the audit or replace the GitHub history checks.

## Acceptance Threshold

Before editing, establish all of the following:

1. **Concrete benefit:** identify a specific porting obstacle, materially duplicated algorithm, or missing behavioral test for an existing Go capability. Explain which maintenance work the proposal eliminates or which uncovered contract it protects; "closer to .NET" or "easier future ports" is not enough.
2. **Verified evidence:** inspect the corresponding source and relevant tests at the pinned upstream commit and the Go implementation/callers. Cite the exact files, symbols, upstream commit or PR when applicable, and behavioral scenario. A matching name or path is not evidence.
3. **Simpler Go or useful coverage:** consolidate meaningful duplicated logic, remove a demonstrated obstacle to applying an upstream change, or adapt a missing upstream behavioral test that the current Go implementation already passes. Check existing helpers and tests first.
4. **Preserved contracts:** identify how tests cover affected behavior, including ownership, ordering, error, or lifecycle paths when relevant. Unexported code and passing unrelated tests do not prove behavior preservation.

Reject single-use helper extraction, renaming, moving short blocks, or adding intermediate slices, passes, or abstractions solely to imitate .NET layout. Do not manufacture duplicate tests to justify such a refactor. Do not turn a test that exposes a Go bug into a behavior-changing fix in this workflow; defer it to the appropriate porting workflow.

## Work Loop

1. Check history and feedback, then inspect at most three promising candidates. Prefer recent upstream changes, concrete difficulties documented in prior ports, and uncovered upstream test scenarios over random files. Stop early when evidence is missing or candidates are already covered; do not expand the search merely to produce a patch.
2. Apply the acceptance threshold before editing. If no candidate qualifies, record the audit outcome and call `noop`.
3. Implement only the strongest qualifying candidate. For production edits, add or identify focused preservation tests and run them before and after the refactor. For test-only work, confirm the new scenario passes against unchanged production code. Adapt upstream behavioral test intent rather than asserting private Go implementation details.
4. Run `gofmt` and targeted unit tests. Use broader unit tests for shared runtime changes, excluding E2E and replay-harness tests. Investigate failures; do not weaken assertions or change behavior to make the candidate pass.
5. Inspect the diff against the stated benefit and contracts. If it changes public API or behavior, adds a feature, or only creates churn, discard the candidate and call `noop`; do not search for replacement busywork.
6. Update the audit record. Call `create_pull_request` at most once for a qualifying, validated change; otherwise call `noop`. Do not claim that a PR exists merely because a write intent was submitted.

## PR Body

Use this shape:

```markdown
## Summary

Describe the scoped code or test change, the concrete porting obstacle or coverage gap, and the maintenance work eliminated or contract protected. Explain why leaving the current Go implementation unchanged is worse.

## .NET Reference

- Upstream commit SHA and immutable source/test links
- Relevant symbols and behavioral scenarios, plus the upstream PR when applicable
- Corresponding Go implementation and existing coverage inspected

## Public API and Behavior

No public Go API changed. No intentional behavior change was made.

## Tests

List the specific behavior covered, tests added or reused, and actual before/after test results. For test-only changes, explain the previously uncovered upstream scenario and confirm production code is unchanged.

## Notes

Link related tracking issues/PRs and relevant prior feedback. Mention skipped candidates, deferred bug/feature ports, and remaining uncertainty.
```

Titles should name the specific behavior or duplicated algorithm, not generic cleanup or alignment.

## No-Change Requirement

Call `noop` without creating an issue when there is no qualifying change. Give a concise audit summary:

- The pinned upstream SHA and inspected commit range or areas, if available
- History/feedback checked, with links to existing work when relevant
- Why inspected candidates did not meet the threshold, including evidence blockers or deferred behavior changes

Do not describe missing evidence as proof that no portability problem exists.