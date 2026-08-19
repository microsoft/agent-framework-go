---
description: Classifies Agent Framework pull request risk when deterministic rules are inconclusive
tracker-id: classify-pull-request-risk
on:
   roles: all
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
         - pending-auto-risk
         - failed-auto-risk
      max: 5
      target: "${{ inputs.pr_number }}"
timeout-minutes: 10
---

# Agent Framework Pull Request Risk Classifier

Classify pull request `${{ inputs.pr_number }}` in `${{ github.repository }}` only when the evidence supports one risk level with high confidence.

The deterministic classifier already handled unambiguous low-risk changes. This agent reviews every production or otherwise ambiguous change. A wrong risk label is worse than abstaining. Before this agent starts, the deterministic stage adds `pending-auto-risk` and clears every existing risk label. A confident classification must therefore actively add one risk label and remove the pending marker.

## Risk Levels

- `risk:low`: Limited blast radius and straightforward rollback. Examples include documentation, comments, examples, tests that do not alter production behavior, isolated internal fixes, and patch dependency updates with no meaningful runtime impact.
- `risk:medium`: Contained production impact with a reasonable rollback. Examples include backwards-compatible API additions, bounded feature or bug-fix behavior, minor dependency upgrades, retries, timeouts, streaming, tool invocation, error handling, or refactoring across several packages.
- `risk:high`: Large blast radius, difficult rollback, security implications, or compatibility risk. Examples include authentication, authorization, secrets, tool permissions or code execution, breaking APIs, core orchestration, checkpointing, persistence, serialization compatibility, significant concurrency changes, major dependency upgrades, or inadequate tests for the possible impact.

## Signals

Use all available evidence rather than treating any single label as decisive:

- Assess the regression risk introduced by this proposed change, not merely how sensitive the touched component is. A small, well-tested safeguard or bounded bug fix in a sensitive area is usually `risk:medium`, not automatically `risk:high`.
- `size:*` indicates review surface, not semantic risk by itself.
- `kind:*` distinguishes code, tests, documentation, examples, dependencies, and CI.
- `area:*` identifies affected Agent Framework subsystems and providers.
- Changed paths reveal workflow state, checkpoint, shell/tool-approval, concurrency, authentication, and serialization boundaries.
- For dependency changes, inspect whether the update is patch, minor, or major and whether it changes runtime behavior.
- `public-api-change` is positive evidence that exported API changed, but it does not prove the change is breaking. Its absence is not proof that no API changed because the parity workflow may still be running.
- Review the actual diff and test changes to distinguish an isolated fix from a broad behavioral change.

## Confidence Gates

Apply a risk label only when the actual diff provides concrete evidence for that level:

- Use `risk:low` only when there is no meaningful production behavior change, or the change is obviously isolated, directly tested, and straightforward to roll back.
- Use `risk:medium` only when production impact is clearly bounded, compatibility is preserved, tests cover the changed behavior, and no high-risk criterion is present.
- Use `risk:high` only when the diff itself shows a concrete high-risk property such as a breaking API, weakened trust boundary, persistence or serialization incompatibility, broad orchestration or concurrency impact, major dependency upgrade, difficult rollback, or inadequate tests for the potential impact. A sensitive path alone is not sufficient.

Abstain by adding `failed-auto-risk` when any of these are true:

- Required files, patches, labels, or dependency-version details cannot be read.
- Classification depends on assumptions not established by the diff.
- More than one risk level remains reasonably plausible.
- The test evidence or rollback scope is unclear.
- You cannot cite concrete evidence for the selected level.

Do not guess, choose a default, or round uncertainty up to a higher risk level.

## Process

1. Use GitHub tools to read the PR title, body, existing labels, changed files, and relevant diff patches. Do not execute pull request code.
2. Apply the confidence gates above. Select exactly one risk level only when one level is clearly supported; otherwise abstain.
3. On success, add the selected risk label, remove the other two risk labels, and remove `pending-auto-risk` and `failed-auto-risk` if present. Do not remove labels outside this allowlist. The safe-output target is already fixed to this PR, so omit `item_number` from label calls. If the tool requires it, pass the bare integer `${{ inputs.pr_number }}`; never pass a string or prefix it with `#`.
4. If the selected risk label is already the only risk label and neither marker is present, use `noop`.
5. Do not add comments or reviews.
6. If the PR cannot be read or classified confidently, add `failed-auto-risk`, remove `pending-auto-risk`, and do not add a risk label.

The workflow validates the final state after this agent finishes. A valid automatic result is either exactly one risk label with no marker, or `failed-auto-risk` with no risk or pending label. Missing, pending, conflicting, or mixed states are converted to the unable marker and fail the workflow check.