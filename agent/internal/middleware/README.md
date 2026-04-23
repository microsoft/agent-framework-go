This package exists to hold middleware implementations that are used by the
`agent` package itself.

Why this is internal:
- Public middleware APIs are defined in `agent` (for example `agent.Middleware`,
	`agent.RunFunc`, and `agent.Option`).
- Some middleware implementations need to be wired in by `agent.New(...)` as
	defaults and therefore are imported by the `agent` package.
- If those middleware implementations were in a package that imported `agent`
	directly, Go would create an import cycle:
	`agent` -> `middleware implementation` -> `agent`.

To avoid that cycle, implementations in this folder depend on internal option
types (`internal/agentopt`) and internal middleware interfaces, and the public
`agent/middleware/...` packages provide thin adapters that expose the same
behavior through the public `agent` API.
