This package exists to hold agent options implementations that are used by the
`agent` package itself.

Why this is internal:
- Public option APIs are exposed from the `agent` package (for example setters
	like `WithSession`, `WithTool`, and `Stream`).
- The `agent` package also imports internal middleware implementations to wire
	default behavior in `agent.New(...)`.
- Those middleware implementations need to read and append run options.

If middleware implementations imported the public `agent` option API directly,
Go would create an import cycle:

`agent -> internal middleware implementation -> agent`

To avoid that cycle, shared option plumbing lives here in an internal package.
The public `agent` option functions are thin wrappers over these internal
types, and adapter packages expose middleware functionality through the public
`agent` surface without creating circular dependencies.
