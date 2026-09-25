---
name: dotnet-symbols
description: "Find .NET/C# Agent Framework (MAF) declarations and Go SDK counterparts with symbolmap. Use for symbol lookup, declaring namespaces, overloads, .NET-to-Go porting or parity reviews, unreviewed declarations, and symbol mapping updates or validation."
argument-hint: "A .NET type/member, qualified Go symbol, or API comparison"
---

# .NET and Go symbol lookup

Look up declarations before inferring a counterpart. Keep symbol lookup read-only: do not edit the mapping, regenerate the inventory, or expand the .NET package scope unless requested.

## 1. Choose the right query

Run from the repository root with its Go toolchain. Start with a filtered CLI query, not a full read of either JSON file. Substitute the requested name for the examples below.

| Need | Query |
| --- | --- |
| Recorded Go counterpart for a .NET member | `go run ./cmd/symbolmap mappings -symbol AIAgent.RunAsync -limit 5` |
| Recorded .NET counterparts for a Go symbol | `go run ./cmd/symbolmap mappings -symbol agent.Session.Get -limit 5` |
| Inventoried .NET methods, whether reviewed or not | `go run ./cmd/symbolmap reconcile -type AgentSession -kind method -limit 5` |
| Current exported Go declarations and signatures | `go run ./cmd/symbolmap go -symbol agent.Session -limit 5` |
| Unreviewed declarations for a specific type | `go run ./cmd/symbolmap reconcile -state unreviewed -type AgentSession -limit 5` |
| Recorded partial or unmapped assessments | `go run ./cmd/symbolmap gaps -type AgentSession -limit 5` |

`mappings` searches only assessed catalog entries. For declaration discovery, use `reconcile` **without status/state filters** so both reviewed and unreviewed APIs remain visible; use `-state unreviewed` only for the review queue. `go` reads the Go module independently of the catalog.

For counts only, use `mappings -summary` or `reconcile -summary`. There is no standalone `summary` command. Flags follow the subcommand; use its `-help` to check supported options.

## 2. Resolve the exact declaration

- `-namespace`, `-type`, and `-symbol` are case-insensitive **substring** filters, not exact matches. Combine them with `-kind` when needed, quote names containing generics or spaces, and inspect the returned declaring namespace and full overload. `-symbol` also searches Go snippets and targets. Inherited members belong to their declaring base type.
- JSON is the default: read `mappings` for catalog results, `rows` for reconciliation, and `symbols` for the Go index. Pages default to 20 rows; the examples request five. Follow `page.next_offset` with the same filters and limit while inputs remain unchanged. Stop when the requested declaration is resolved; traverse all pages only for an exhaustive request. Use `-limit=0` only for a requested full export. Summary counts cover all filtered matches and reject paging flags.
- If a mapping query has `page.total: 0`, try `reconcile` for the containing type without status/state filters, then broaden the type/member query and check declaring ownership. An empty page with a nonzero total can mean the offset is past the end; retry at offset zero. Neither case proves an API is absent from Go.
- Treat command failure separately from zero matches: inspect stderr and the exit status. `go` and `reconcile` index offline and require cached dependencies. If indexing is unavailable, use scoped source/data reads and label what remains unvalidated; do not alter dependencies or regenerate the inventory merely to answer a lookup.

## 3. Verify evidence and answer

- Read `status` and `note` for the exact leaf. `go_symbols` records counterparts; `go` is illustrative code, not a standalone program. Free variables are allowed and their receiver/argument types are not fully checked. Verify Go signatures and actual callers before using a snippet in an implementation. Properties intentionally have no example.
- Reconciliation `state` is not mapping `status`: `linked` means references resolve, not behavioral parity; `unreviewed` means no assessment, not `unmapped`. `suggested_go` is only a name-based search hint. A mapped type does not cover all its members.
- The generated inventory covers three core .NET packages, not every integration or external dependency. `outside-inventory-scope` is not evidence of removal. `go-outside-scope` means a target package was not indexed. Go results cover the reported build configuration, not every platform. An unresolved identity or unvalidated target is not a confirmed implementation gap.
- For a behavioral claim, inspect .NET source at the applicable recorded revision and compare the Go implementation, callers, and tests. A leaf's `review` selects `reviews[review]`; absence selects `baseline`, not the containing type's review. Check inventory/build provenance, `review_source_changed`, `review_go_changed`, and Go dirty state before presenting an old assessment as current. Missing change flags are unknown, not proof of an unchanged revision.

Answer concisely with the exact .NET declaration, recorded Go counterpart(s), and the relevant assessment or scope limitation. Cite source and revision for behavioral claims; distinguish recorded mappings from newly verified behavior. Do not dump the full JSON response or describe unreviewed/out-of-scope entries as missing features.

## 4. Update only when requested

- Change only the assessed leaf under its declaring namespace/type and exact overload. Keep `go_symbols`, `status`, and a concrete `note`; do not promote a name suggestion into a mapping without source evidence.
- Non-property `go` examples use minimal calls or literals, conventional variables, and `new(value)` for pointers—not synthetic outer functions or unrelated fields. Real callbacks may remain. Property leaves omit `go` entirely.
- Preserve existing reviews and baselines; record newly inspected work in a separate review batch. Never hand-edit the generated inventory or maintain a second mapping in the skill.

After mapping-only changes, run `go run ./cmd/symbolmap reconcile -summary -check`. Run `go test ./cmd/symbolmap` and `go vet ./cmd/symbolmap` when changing the command's Go code, not for catalog-only edits. Strict checks inspect the whole unfiltered report; a paged result or zero exit status does not establish behavioral parity or complete package coverage.

## References, loaded as needed

- [Mapping guide](../../../docs/dotnet-go-sdk-feature-comparison.md): schema, statuses, and CLI contracts.
- [Reviewed mapping](../../../docs/dotnet-go-sdk-symbol-mapping.json): scoped assessment edits.
- [Generated inventory](../../../docs/dotnet-sdk-symbol-inventory.json): canonical CLR identities and package/assembly provenance.
- [Extractor guide](../../../cmd/dotnetsymbols/README.md): pinned regeneration, only when requested.
