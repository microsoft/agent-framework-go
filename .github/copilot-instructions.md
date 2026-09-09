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
