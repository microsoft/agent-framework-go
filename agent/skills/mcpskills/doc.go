// Copyright (c) Microsoft. All rights reserved.

// Package mcpskills discovers skills served by a trusted Model Context Protocol
// (MCP) server. It reads the skill://index.json discovery document and supports
// skill-md and ZIP archive entries.
//
// Skill-md discovery uses index metadata without downloading SKILL.md. The first
// successful GetContent call caches the full instructions; GetResource fetches
// named sibling resources on demand. Resources may contain text or binary
// [github.com/microsoft/agent-framework-go/message.DataContent].
//
// Archives are refreshed on every discovery, with optional SHA-256 digest
// verification and configurable download, file-count, and uncompressed-size
// limits. Extraction is confined to each skill directory. Extracted scripts are
// never executable; configure resource extensions to make script files readable.
// Archive containers use safe directory names, independently of the names of
// skills within them. Archive resource reads are serialized with refresh and
// cleanup; waiting reads honor context cancellation.
// Unsupported entry types, including mcp-resource-template, are skipped.
//
// Register a source with [github.com/microsoft/agent-framework-go/agent/skills.ContextProviderOptions.Sources].
// The provider caches discovery by default; configure its RefreshInterval or
// DisableCaching when server content changes. Resource reads continue to use the
// originating server even when discovery is cached.
//
// The caller owns the MCP session. Close the source before closing the session,
// or transfer source ownership to the skills provider with OwnsSources.
// Only connect to trusted servers: remote instructions and resources may contain
// prompt injection. Digests verify consistency with the server's index, not
// authenticity or trustworthiness.
package mcpskills
