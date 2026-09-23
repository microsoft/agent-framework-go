# Copilot instructions

## Never commit compiled binaries or build artifacts

Do not stage, commit, or push compiled binaries or build artifacts. This
includes, but is not limited to:

- Extensionless `go build` output named after its source directory
  (for example, `examples/02-agents/agents/step10_as_mcp_tool/step10_as_mcp_tool`).
- `go test -c` binaries (`*.test`) and coverage output (`*.out`, `coverage.html`).
- Platform executables and libraries (`*.exe`, `*.dll`, `*.so`, `*.dylib`).
- Anything under `bin/`, `dist/`, `vendor/`, `tmp/`, or `temp/`.

Rules:

- When building or running examples locally, keep the compiled output out of
  Git. Never use `git add -f` / `--force` to add an ignored artifact.
- Before committing, verify no binary content is staged. A quick check:
  `git diff --cached --numstat` shows `-` for both columns on binary files, and
  `git ls-files --cached` should list only source, config, and docs.
- Compiled binaries are reproducible from source; they must be built by the
  consumer, not checked in.

## Pull request reviews

Use the following guidance when reviewing a pull request. Check callers and
documented contracts before reporting a defect. Explain the concrete failure or
design inconsistency and suggest a scoped fix; do not turn hypothetical misuse
into a requirement for additional defensive code.

### API design and simplicity

- Keep the public surface small. Question new exports, aliases, callback types,
  or wrappers used only by examples, tests, or internal plumbing when existing
  APIs already cover the need. Do not request new public APIs just for samples.
- Follow neighboring APIs: constructor configuration is generally `Config`
  passed by value; ordinary accessors do not need a `Get` prefix. Keep middleware
  parameter ordering consistent. Do not mechanically rename existing APIs.
- For optional configuration fields, preserve the distinction between unset and
  explicit `false` or `0`. Use pointers when that distinction matters, not when
  nil and the zero value mean the same thing.
- Prefer existing standard-library, SDK, and package helpers, such as
  `slices.Clone`, `agent.GetOption`, `message.NewText`, and SDK stream accumulators.
  Do not replace clear loops with more verbose callback helpers or extract
  forwarding helpers that add no meaning.

### Semantic parity and compatibility

- For upstream ports, check stable .NET Agent Framework and Microsoft.Extensions.AI
  semantics, including defaults, opt-in gates, errors, and lifecycle behavior.
  Flag experimental upstream API additions without explicit maintainer agreement.
- Match observable behavior, not language-specific API shapes. Local JSON field
  casing, GUID formatting, and CLR timeout sentinels need not be copied into Go.
  Provider wire formats still have to satisfy their protocols. Do not copy a
  demonstrated upstream bug merely for parity.
- Support parity findings with concrete upstream evidence available to the
  review. Similar names or a passing happy-path test do not establish equivalence;
  lack of upstream evidence does not establish a mismatch.
- Preserve public signatures and behavior unless a breaking change has explicit
  maintainer agreement. Observable contracts include concrete types stored in
  `any`, defaults, and serialized values, not just function signatures.

### Ownership and execution

- Middleware and context-provider inputs are read-only. Flag writes to shared
  messages, slices, options, or schemas without first taking ownership. Do not
  request blanket defensive copies solely to protect against callers violating
  documented read-only contracts.
- Keep provider-specific schema and wire transformations at provider boundaries.
  Preserve the original schema for local validation and other providers; do not
  change shared schema semantics to accommodate one provider.
- Trace affected behavior through streaming/non-streaming and sequential/concurrent
  paths. For tool middleware, include configured tools, context-provider tools,
  additional tools, and provider-managed execution. Check for bypassed middleware
  and lost approval requirements.
- Respect error contracts: do not mask original failures with cancellation,
  cleanup errors, or successful-looking decoding fallbacks. Check that early
  stream termination and error returns release the underlying run and resources.
- Check nested calls and repeated runs for stale call/user identity, cross-session
  state leakage, duplicated retained history, and lost ordering, call/result
  pairing, metadata, or usage during stream assembly.

### Tests and performance

- Prefer black-box regression tests through public APIs. Assert observable results,
  errors, and events rather than private state or incidental representations.
  Extend existing component test files instead of adding one file per bug, unless
  isolation or build constraints justify a separate file.
- Cover the affected failure and lifecycle paths, not only successful helpers.
  For ports, adapt relevant upstream behavioral tests. Synchronize concurrency
  tests with channels, wait groups, or `testing/synctest`, not sleeps or arbitrary
  deadlines, unless timing itself is the behavior under test.
- Support performance findings with a relevant benchmark/profile or a concrete
  cost on the affected path. Do not trade a simple implementation for extra
  machinery based on speculative allocation savings.

### Documentation and change scope

- Public Go doc comments should explain Go behavior, defaults, and ownership,
  not .NET comparisons or porting history. Keep cross-language rationale in PR
  discussion or internal implementation comments. Use Go doc symbol links.
- Keep findings and suggested fixes within the PR's purpose. Flag unrelated
  additions; do not request unrelated refactors, renames, existing-test
  reorganization, or speculative compatibility layers.
- For generated gh-aw workflows, check that changes come from source/configuration
  updates or an intentional generator upgrade, rather than manual generated-file
  edits that the next compilation will erase.
