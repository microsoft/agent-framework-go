# Verify Examples

`verifyexamples` runs selected Go examples and verifies their output. It is the Go port of the corresponding .NET example verifier.

The .NET verifier is the reference shape for this command: explicit example sets, category filtering, deterministic substring checks, optional AI verification for nondeterministic output, per-example stdin, environment-based skips, and optional log/CSV/Markdown output.

## Usage

Run from the repository root:

```
go run ./cmd/verifyexamples
```

Run specific examples by name:

```
go run ./cmd/verifyexamples 01_get_started_05_first_workflow 03_workflows_loop
```

Run a category:

```
go run ./cmd/verifyexamples --category 01-get-started
go run ./cmd/verifyexamples --category 02-agents
go run ./cmd/verifyexamples --category 03-workflows
```

Useful options:

```
go run ./cmd/verifyexamples --parallel 16
go run ./cmd/verifyexamples --log results.log
go run ./cmd/verifyexamples --csv results.csv
go run ./cmd/verifyexamples --md results.md
go run ./cmd/verifyexamples --build
```

By default, example processes run with `go run -mod=readonly .`. Passing `--build` uses plain `go run .`.

## Security Notice

`verifyexamples` executes example programs from this repository. Review any example before adding it to the registry, especially if it starts servers, opens files, uses shell commands, or calls external services.

Some examples and AI verification use credentials from environment variables. Do not put secrets in example inputs, expected output, logs, CSV/Markdown reports, or committed files. AI verification may send captured stdout/stderr to the configured Foundry model, so examples that print sensitive values should be skipped or rewritten before being registered.

## Verification

Each example definition can include:

- `MustContain`: substrings that must appear in stdout.
- `MustNotContain`: substrings that must not appear in stdout.
- `IsDeterministic`: skips AI verification when exact checks are sufficient.
- `ExpectedOutputDescription`: semantic expectations for AI verification.
- `Inputs` and `InputDelay`: lines to send to stdin for interactive examples.
- `RequiredEnvironmentVariables`: missing values skip the example.
- `OptionalEnvironmentVariables`: missing values also skip when they would cause prompts or nondeterministic hangs.
- `SkipReason`: structural skip reason for servers, multi-process examples, or external service requirements.

AI verification is enabled when `FOUNDRY_PROJECT_ENDPOINT` is set. `FOUNDRY_MODEL` is optional and defaults to `gpt-4o-mini`.

## Example Registry

All example definitions live in `examples.go`, grouped into these static sets:

- `getStartedExamples`
- `agentsExamples`
- `workflowExamples`

`example_sets.go` only contains helper logic for category lookup and `inputLines`. Do not add filesystem discovery for examples; this command should stay aligned with the .NET verifier, where known examples are registered explicitly.

When adding an example:

1. Prefer the matching .NET verifier entry as the behavioral source of truth.
2. Map it to the closest existing Go example path.
3. If the Go example has drifted from the .NET scenario, fix the example when practical instead of weakening the verifier expectation.
4. Use deterministic `MustContain` checks for local or stable examples.
5. Use `ExpectedOutputDescription` for LLM output, with env gates so examples skip cleanly when credentials are missing.
6. Avoid registering servers, clients that require separately running servers, or examples that do not terminate.

Run the focused tests after editing the registry:

```
go test ./cmd/verifyexamples
```

For runnable local examples, also run a targeted verifier command:

```
go run ./cmd/verifyexamples <example_name> --parallel 1
```
