---
description: Classifies Agent Framework pull request risk when deterministic rules are inconclusive
tracker-id: classify-pull-request-risk
on:
   workflow_call:
      inputs:
         pr_number:
            description: "Pull request number to classify"
            required: true
            type: number
   workflow_dispatch:
      inputs:
         pr_number:
            description: "Pull request number to classify"
            required: true
            type: number
concurrency:
   group: "gh-aw-${{ github.workflow }}-${{ github.repository }}-${{ inputs.pr_number }}"
   cancel-in-progress: true
permissions:
   contents: read
   issues: read
   pull-requests: read
   copilot-requests: write
inlined-imports: true
network: defaults
tools:
   cli-proxy: true
   github:
      mode: gh-proxy
      toolsets: [default]
      min-integrity: unapproved
safe-outputs:
   noop:
      report-as-issue: false
   add-labels:
      allowed:
         - risk:low
         - risk:medium
         - risk:high
         - failed-auto-risk
      # A successful classification adds one risk; a failure adds only its marker.
      max: 1
      target: "${{ inputs.pr_number }}"
   remove-labels:
      allowed:
         - risk:low
         - risk:medium
         - risk:high
         - failed-auto-risk
      max: 4
      target: "${{ inputs.pr_number }}"
timeout-minutes: 10
---

# Agent Framework Pull Request Risk Classifier

Classify pull request `${{ inputs.pr_number }}` in `${{ github.repository }}` with exactly one `risk:*` label.

The deterministic classifier already handled unambiguous low- and high-risk changes. This agent runs only when repository-specific rules were inconclusive.

## Risk Levels

- `risk:low`: Limited blast radius and straightforward rollback. Examples include documentation, comments, examples, tests that do not alter production behavior, isolated internal fixes, and patch dependency updates with no meaningful runtime impact.
- `risk:medium`: Contained production impact with a reasonable rollback. Examples include backwards-compatible API additions, bounded feature or bug-fix behavior, minor dependency upgrades, retries, timeouts, streaming, tool invocation, error handling, or refactoring across several packages.
- `risk:high`: Large blast radius, difficult rollback, security implications, or compatibility risk. Examples include authentication, authorization, secrets, tool permissions or code execution, breaking APIs, core orchestration, checkpointing, persistence, serialization compatibility, significant concurrency changes, major dependency upgrades, or inadequate tests for the possible impact.

## Signals

Use all available evidence rather than treating any single label as decisive:

- `size:*` indicates review surface, not semantic risk by itself.
- `kind:*` distinguishes code, tests, documentation, examples, dependencies, and CI.
- `area:*` identifies affected Agent Framework subsystems and providers.
- Changed paths reveal workflow state, checkpoint, shell/tool-approval, concurrency, authentication, and serialization boundaries.
- For dependency changes, inspect whether the update is patch, minor, or major and whether it changes runtime behavior.
- `public-api-change` is positive evidence that exported API changed, but it does not prove the change is breaking. Its absence is not proof that no API changed because the parity workflow may still be running.
- Review the actual diff and test changes to distinguish an isolated fix from a broad behavioral change.

## Process

1. Use GitHub tools to read the PR title, body, existing labels, changed files, and relevant diff patches. Do not execute pull request code.
2. Select exactly one risk level using the definitions and signals above. When evidence falls between levels, choose the higher level needed for safe review depth.
3. On success, add the selected risk label if missing, remove the other two risk labels, and remove `failed-auto-risk` if present. Do not remove labels outside this allowlist.
4. If the selected risk label is already the only risk label and no failure marker is present, use `noop`.
5. Do not add comments or reviews.
6. If the PR cannot be read or classified confidently, preserve existing risk labels and add `failed-auto-risk`.