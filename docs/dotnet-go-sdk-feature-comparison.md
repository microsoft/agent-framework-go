# .NET and Go SDK Symbol Mapping

The comparison is maintained in [dotnet-go-sdk-symbol-mapping.json](dotnet-go-sdk-symbol-mapping.json), grouped by **namespace → short .NET type → members**. Each assessed entry names its Go counterparts rather than describing a broad feature area. The generated [declaration inventory](dotnet-sdk-symbol-inventory.json) supplies the review queue; this page is the guide, not another mapping.

> The inventory is incomplete. The baseline pins the revisions inspected, but symbol correspondence does not establish behavioral parity. A type mapping does not claim that all its members, overloads, defaults, or lifecycle behavior are supported. An unlisted symbol has not been assessed; it is not automatically missing.

## Structure

The catalog uses schema 0, with a required `schema_version` field. It contains one original `baseline`, optional named `reviews`, and `namespaces`. Each namespace contains short declaring-type keys. For example, namespace `Microsoft.Agents.AI` contains `AgentResponse`, whose `properties.Text` maps to `agent.Response.String`.

| Level | Fields | Meaning |
| --- | --- | --- |
| Baseline | Repositories, commits, review date, Go module, scope, `inventory_complete` | Shared provenance. No paths or line numbers are stored per symbol. |
| Namespace | Namespace key containing type entries | Written once instead of repeated in every type and signature. |
| Type | Short type key, `area`, optional `assembly` | Generic parameters and nested declaring types remain in the key, such as `AgentResponse<T>` and `AIContextProvider.InvokingContext`. Assembly identifies extraction scope, not a source-file path. |
| Type mapping | Optional `mapping` | Maps the type itself. Omit this when the type is only a container for assessed members. |
| Member groups | `properties`, `methods`, `constructors`, `fields`, `constants`, `events` | Group names supply the .NET declaration kind. Empty groups can be omitted. |
| Member key | Relative name or short overload signature | For example, `Text` or `RunAsync(string, AgentSession, AgentRunOptions, CancellationToken)`. Qualify parameter types only when their short names are ambiguous. |
| Mapping leaf | `go_symbols`, `status`, `note`, optional `go` and `review` | `go_symbols` records counterparts for validation and search. Types, methods, constructors, fields, constants, and events also carry a minimal `go` example when mapped. Property leaves never carry examples. `review` selects a named review batch; nothing is inherited from the containing type. |
| Review batch | Date, .NET/Go commits, inventory hash, scope | Records a newly inspected subset without advancing the baseline of old assessments. |

Go symbols combine the module-relative package and exported symbol: `agent.Agent`, `agent.Response.String`, or `workflow/inproc.ExecutionEnvironment.Run`. The module prefix is recorded once in `baseline.go_module`. One .NET declaration can reference several Go symbols. `go_symbols` is always an array; it is empty only for `unmapped` or `intentional` declarations.

For non-property declarations, `go` is a minimal Go snippet showing the correspondence. It is an empty string only when no counterpart is recorded. Property mappings intentionally omit `go`: their `go_symbols` identify the field, accessor, or composed API without adding a contrived usage example. Examples contain no imports or synthetic outer function; conventional free identifiers such as `ctx`, `session`, and `logger` denote caller-supplied values. Reconciliation resolves and type-checks package-qualified APIs against the indexed Go API without executing them.

### Symbol identity

- Use the declaring .NET namespace, not the project/assembly name. For example, `CosmosChatHistoryProvider` is declared in `Microsoft.Agents.AI`.
- Keep overloads separate. Use source-like generics and `ref`/`out`/`in`, without parameter names, defaults, `this`, or `params`. A constructor key is `TypeName(ParameterTypes)`. Reference-nullability annotations do not distinguish overloads; nullable value types still do. Reports omit reference-nullability annotations rather than claiming to reconstruct C# source.
- The generated inventory retains canonical CLR identities. Reconciliation resolves the catalog's exact namespace and short declaring type, then resolves member signatures. Generic parameter names match by position. It never chooses an arbitrary same-named type or overload.
- Short parameter names are preferred; nested names retain their declaring type. Ambiguous parameter names require a namespace suffix. A return type is only needed for return-only overloads. Unsupported signature spellings remain canonical or unresolved, never silently simplified.
- Put inherited members under their actual declaring type rather than duplicating them under every subclass. One mapping can list several Go counterparts where Go declares methods directly instead of inheriting them.
- Member names and their containing type form a stable fully qualified symbol for reports and issue references. Do not infer coverage from matching names alone.

## Statuses

| Status | Meaning |
| --- | --- |
| `mapped` | A direct API counterpart was identified. Ordinary naming, property/accessor, or language-type differences may remain. This is not a behavioral-parity claim. |
| `adapted` | The counterpart uses Go composition or a different API shape, such as middleware, options, function fields, or a factory. This alone is not a gap. |
| `partial` | A counterpart exists but the note identifies a specific unsupported aspect of the recorded declaration. |
| `unmapped` | The upstream declaration exists, but no Go counterpart was located. Scope, stability, and applicability still need review. |
| `intentional` | A confirmed decision explains why no direct counterpart is planned. The note must identify that decision; do not infer intent from absence or inherit it from an old comparison. |

Status belongs to each mapping leaf. A mapped type can contain unmapped members, and a member-only type container makes no claim about a whole-type counterpart. Language-specific machinery such as DI, class inheritance, or serializer options is not automatically a defect.

## Query the Mapping

Run from the repository root with `go run ./cmd/symbolmap <subcommand> [flags]`. The [symbolmap command](../cmd/symbolmap/main.go) returns JSON by default and limits row-producing responses to 20 matches per page. Use filters before requesting more rows. `mappings` and `gaps` only read the catalog; `go` indexes the Go module without loading the catalog; `reconcile` reads the catalog and declaration inventory and indexes Go. Go indexing invokes the local toolchain and reads Git metadata. These subcommands never write the catalog or access the network themselves.

Agents can start with the short [symbol lookup skill](../.github/skills/dotnet-symbols/SKILL.md); this guide remains the detailed reference.

| Question | Command |
| --- | --- |
| Show the first page of recorded mappings | `go run ./cmd/symbolmap mappings` |
| Summarize explicit symbols by area, kind, and status | `go run ./cmd/symbolmap mappings -summary` |
| Inspect a type's properties | `go run ./cmd/symbolmap mappings -type AgentResponse -kind property` |
| Restrict a namespace | `go run ./cmd/symbolmap mappings -namespace Compaction` |
| Find the Go counterpart of a .NET symbol | `go run ./cmd/symbolmap mappings -symbol AIAgent.RunAsync` |
| Find .NET symbols that use a Go counterpart | `go run ./cmd/symbolmap mappings -symbol agent.Session.Get` |
| List workflow gap candidates | `go run ./cmd/symbolmap gaps -area workflows` |
| List declarations with no located counterpart | `go run ./cmd/symbolmap mappings -status unmapped` |
| Inspect a type's mappings as JSON | `go run ./cmd/symbolmap mappings -type AgentRunOptions` |

Flags follow the subcommand. Use `-help` on a subcommand for its supported flags. `mappings -summary` reports counts instead of declaration rows. Mapping filters intersect: `-area`, `-kind`, and `-status` use the documented values; `-namespace`, `-type`, and `-symbol` are case-insensitive substring searches. `-symbol` accepts short or namespace-qualified .NET names and combined Go names. The `gaps` subcommand selects only `partial` and `unmapped`, not `adapted` or `intentional`.

JSON is the default for every subcommand; use `-json=false` for human-readable text. Row-producing JSON responses (`mappings`, `gaps`, `go`, and `reconcile` without `-summary`) include `page` with `total` matches after filtering, `offset`, `limit`, and `returned`. When `page.next_offset` is present, repeat the same filters with `-offset` set to that value; its absence means there are no more matches. `-limit` defaults to 20; use `-limit=0` explicitly to export every filtered match. `-limit` and `-offset` cannot be combined with `-summary`, whose counts always cover the entire filtered selection. Text reports show the same page information.

Mapping output includes the baseline, named reviews, and a flat `mappings` array with short `dotnet` labels, their `namespace`/`assembly`, and explicit assessments. The hierarchy remains the single maintained source. Summary counts include only explicit mappings, not grouping-only type containers, and deduplicate Go targets shared by several .NET declarations. Counts are not a parity percentage or a count of implementation tasks.

## Extract the .NET Declaration Inventory

The generated [dotnet-sdk-symbol-inventory.json](dotnet-sdk-symbol-inventory.json) snapshot contains the public declarations from the three core MAF packages. Its package and assembly metadata records the exact release, target framework, source feed, and hashes. It does not include every provider integration or external dependency.

The separate [dotnetsymbols command](../cmd/dotnetsymbols/README.md) extracts an inventory from compiled .NET assemblies using `go-winmd`. By default, `go run ./cmd/dotnetsymbols` resolves the latest stable MAF release and downloads its prebuilt core NuGet packages, without Git or a .NET SDK. Pin a version with `-release 1.22.0` for reproducible reruns. Select additional packages with `-package` and an exact target framework with `-framework`. Package versions, hashes, source metadata, and assembly identities are recorded; the release is not assumed to match this catalog's commit baseline.

For unpublished source, build the relevant upstream library projects separately and pass their reference or implementation assemblies with `-assembly`. Both modes include types, declared members, overloads, events, generic constraints, and experimental/compiler-generated markers without executing assembly code. The snapshot omits metadata not used for reconciliation, such as parameter names/defaults, constant values, raw attribute lists and unused API flags. No per-symbol paths or line numbers are stored.

Keep this generated inventory separate from the reviewed mapping: extraction cannot decide Go counterparts or parity status. Metadata uses canonical CLR signatures; the catalog uses short source-like names. The `reconcile` subcommand resolves these formats without rewriting assessments. Unreviewed declarations are not `unmapped`. See the extractor guide for selection rules, parser limitations, and requirements. The extractor is Go-only and never builds .NET projects.

## Reconcile the Inventories

The Go index reads exported types, functions, methods, fields, constants, and variables from the SDK packages. It follows pointer receivers, aliases, and unambiguous promoted members. It excludes main/internal packages and tests, and never executes package initializers. Symbols remain combined names such as `agent.Agent.ID`; no per-symbol paths or lines are recorded.

| Question | Command |
| --- | --- |
| Summarize the current Go public API | `go run ./cmd/symbolmap go -summary` |
| Inspect actual Go signatures | `go run ./cmd/symbolmap go -symbol agent.Session` |
| Export the complete Go index | `go run ./cmd/symbolmap go -limit=0` |
| Summarize inventory reconciliation | `go run ./cmd/symbolmap reconcile -summary` |
| List the review queue for a type | `go run ./cmd/symbolmap reconcile -state unreviewed -type AgentSession` |
| Find broken or ambiguous .NET references | `go run ./cmd/symbolmap reconcile -state needs-reconciliation` |
| Find invalid Go targets | `go run ./cmd/symbolmap reconcile -state invalid-go-target` |
| Validate all recorded in-scope references | `go run ./cmd/symbolmap reconcile -summary -check` |
| Get the next review-queue page with provenance | `go run ./cmd/symbolmap reconcile -state unreviewed -offset 20` |

Reconciliation states are separate from mapping statuses:

| State | Meaning |
| --- | --- |
| `linked` | The assessment references one inventory declaration, and all recorded Go targets exist. An assessed empty target list is valid; linked does not mean implemented. |
| `unreviewed` | An inventoried declaration has no uniquely linked assessment. Its status remains absent, not `unmapped`. |
| `needs-reconciliation` | A declaring type/member is absent, ambiguous, or claimed by conflicting assessments. Inspect selection, visibility, source revisions, and spelling; do not assume removal. |
| `invalid-go-target` | A recorded target is absent from the selected Go API. |
| `outside-inventory-scope` | The recorded assembly or namespace was excluded from extraction. These rows are retained, not counted as removed APIs. |
| `go-outside-scope` | A target package was not indexed under an explicit `-go-package` selection. Its existence remains unverified. |

Unresolved assessments never consume an unreviewed declaration. The queue includes type declarations even when only members have assessments. Experimental markers and compiler-generated/delegate machinery are identified separately. `suggested_go` contains name-based candidates, preferably under an existing type mapping; candidates are not assessments and can be unrelated APIs.

`reconcile -inventory` selects a different declaration snapshot; `go` and `reconcile` accept `-go-root` to select a Go checkout. The standalone `go` subcommand discovers its module from that checkout, without reading the mapping catalog; `reconcile` also checks that the module matches the catalog baseline. Repeat `-go-package` to replace the default agent/message/tool/provider/workflow package set, and use `-tags` for an explicit build-tag selection. The report records the effective platform, architecture, CGO setting, Go version, tags, Git commit and dirty state. It describes one build configuration, not the union of all platforms. Missing dependencies or invalid packages fail indexing rather than producing misleading missing-symbol results.

Indexing requires the local Go toolchain and cached dependencies. It disables module/toolchain downloads and uses read-only module loading; prepare dependencies separately if needed. The outer `go run` itself follows the caller's ordinary Go environment. Configured custom tool/cache programs are rejected by the indexer. No .NET SDK is used.

`-check` checks the entire report, even when filters or pagination hide failing rows. It fails on unresolved .NET references or invalid Go targets, including invalid Go targets on out-of-scope .NET rows. Being unreviewed or outside the .NET inventory scope alone does not fail this reference check. The report is still emitted before the nonzero exit status. `-summary` omits rows but retains selection provenance; counts apply to the entire filtered selection, not just one page, while `inventory_declarations` records the complete input size.

Existing reviews retain their baselines and the hashes of the snapshots originally inspected, even when a snapshot is reformatted or trimmed. A linked declaration can still have `review_source_changed` or `review_go_changed`; these compare commits, not behavior. A dirty Go checkout also prevents treating HEAD alone as an exact snapshot. Matching identities does not certify old assessments against a newer release.

## Derive and Maintain Work

1. Start with `go run ./cmd/symbolmap reconcile -state unreviewed` and inspect declarations one type at a time. Resolve any .NET candidates and inspect suggested Go APIs, their callers, documentation and tests. Record one-to-many Go compositions where appropriate; do not derive a status from name similarity.
2. Only after assessment, consider `partial` or `unmapped` declarations as work candidates. Check experimental gates and open/closed issues or PRs. Several declarations may belong to one task; deduplicate related constructors, options, and types before proposing work.
3. Confirm the missing observable behavior and choose a narrow change with behavioral tests. An API adaptation may already cover the use case; new public Go APIs are not required just to resemble .NET.
4. Update the specific type/member mapping. Add an unlisted overload separately, preserve actual declaring ownership, and record any concrete limitation without asserting all-member parity.
5. Run `go run ./cmd/symbolmap mappings -summary` and `go run ./cmd/symbolmap reconcile -summary -check`. The first validates schema, duplicate keys/symbols, overload formats and Go target syntax; reconciliation additionally resolves .NET declarations and checks actual Go symbols. Command tests can be run with `go test ./cmd/symbolmap`. No provider or end-to-end runs are needed for a catalog-only edit.

Keep the baseline tied to the revisions actually inspected. New assessments should reference a named review batch with the inventory hash and inspected source commits; old leaves keep their original baseline. Reformatting or grouping does not refresh verification, and inspecting a subset does not justify advancing the whole catalog's baseline. Record supporting source/test evidence and any approved omission in the change's PR discussion. The previous broad feature comparison remains available in Git history.

Existing links to this guide remain valid. Porting workflows should update the relevant entries in the linked symbol mapping rather than append another feature table or package checklist here.

## Weekly Mapping Maintenance

The [weekly workflow](../.github/workflows/symbolmap-maintenance-weekly.md) runs weekly and supports manual dispatch. Each run checks every existing assessed mapping for staleness from recent Go work or preexisting inaccuracies; it does not rotate through a subset. Revision or review-date changes alone do not establish staleness.

Its draft PRs can change only the reviewed mapping catalog. The .NET declaration inventory is a read-only reference: the workflow does not regenerate it, query newer .NET releases, or assess whether it is stale. Inventory maintenance remains manual. SDK changes, package expansion, and filling the unreviewed declaration backlog are outside this workflow's scope. Open results do not prevent subsequent audits; duplicate corrections are skipped. A complete audit with no new substantiated correction reports a no-op; an unfinished audit reports itself as incomplete.

The workflow prepares Go dependencies and the full past-week SDK commit list before the agent runs. Source inspection uses recorded revisions when needed; no separate preparation command or .NET inventory update is involved.
