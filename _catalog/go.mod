// Development-only tooling and data, excluded from the root SDK module ZIP.
// This module is not an SDK dependency or a separately released module.
module github.com/microsoft/agent-framework-go/_catalog

go 1.26.0

require (
	github.com/microsoft/go-winmd v0.0.0-20260922124842-16e7d31aeb8a
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba
	golang.org/x/mod v0.41.0
	golang.org/x/tools v0.51.0
)

require golang.org/x/sync v0.23.0 // indirect
