# .NET and Go SDK Symbol Mapping

The comparison is maintained in the [symbol catalog](../_catalog/dotnet-go-sdk-symbol-mapping.json), grouped by **namespace → short .NET type → members**. Each assessed entry names its Go counterparts rather than describing a broad feature area. The same catalog stores .NET declaration labels, extraction provenance, and test identities; this page is the guide, not another mapping.

> API extraction scope is incomplete. The original baseline records historical assessment provenance, not the latest inspection date or revisions of every current assessment. Symbol correspondence does not establish behavioral parity. A type mapping does not claim that all its members, overloads, defaults, or lifecycle behavior are supported. Unreviewed declarations, unlisted symbols, and missing extraction metadata are not evidence of missing features.

## Structure

The catalog uses schema 0, with a required `schema_version` field. It preserves one original `baseline` and `go_only` assessments, alongside `dotnet` extraction metadata, `tests` identities and pairs, and `namespaces`. There are no named review records, per-leaf review references, or Go test review lists. Each namespace contains short declaring-type keys. For example, namespace `Microsoft.Agents.AI` contains `AgentResponse`, whose `properties.Text` maps to `agent.Response.String`. Both commands use the [shared catalog model](../_catalog/cmd/internal/symbolcatalog/model.go) as their schema source of truth.

| Level | Fields | Meaning |
| --- | --- | --- |
| Baseline | Repositories, commits, review date, Go module, scope, `inventory_complete` | Original historical provenance, not a current assessment date for every leaf. No paths or line numbers are stored per symbol. |
| .NET metadata | `dotnet.schema_version: 1`, `identity_format: ecma335-v1`, `sha256`, `selection`, `packages`, `assemblies` | Extraction provenance, separate from the original assessment baseline. `sha256` identifies the raw extraction snapshot used for the refresh, not the catalog's bytes. |
| Test metadata | Optional `dotnet.tests`: `identity_format: test-name-v1`, `assemblies`, optional `unavailable` | Assembly build provenance without duplicated test-name lists. `unavailable[assembly][CLR type]` lists retained paired names absent from a refreshed build. |
| Namespace | Namespace key containing type entries | Written once instead of repeated in every type and signature. |
| Type | Short type key, `area`, optional `assembly`, `kind`, and extraction markers | Source-like keys retain generics and nested declaring types, such as `AgentResponse<T>` and `AIContextProvider.InvokingContext`. `class` is the default kind; only other kinds need recording. Assembly identifies extraction scope, not a source-file path. |
| Type mapping | Optional `mapping` | Maps the type itself or stores its unreviewed placeholder. A grouping-only container makes no type assessment. |
| Member groups | `properties`, `methods`, `constructors`, `fields`, `constants`, `events` | Group names supply the .NET declaration kind. Empty groups can be omitted. |
| Member key | Relative name or short overload signature | For example, `Text` or `RunAsync(string, AgentSession, AgentRunOptions, CancellationToken) -> Task<AgentResponse>`. Every API method key includes its return type. Qualify type names only when their short names are ambiguous. |
| Mapping leaf | `go_symbols`, `status`, optional `go`, `note`, `unreviewed`, and extraction markers | `mapped` leaves normally omit `note`; other assessed statuses require a concrete explanation. Non-property leaves carry a minimal `go` example when mapped; properties never carry examples. |
| Extraction markers | Optional `identity`, `experimental`, `compiler_generated`, `unavailable` on types/members | Only exceptions are stored. `identity` is a canonical CLR override when the surrounding label cannot uniquely identify a declaration during refresh; type overrides are relative to the namespace. `unavailable: true` retains an assessed declaration absent from a refreshed scope. |
| Go-only assessment | Canonical Go symbol key → `note` | Records a source-reviewed Go-specific API with no meaningful .NET counterpart in the reviewed scope. The explanation is required. |
| Test declaration / pair | Assembly → fully qualified CLR declaring type → test name → Go test string or `null` | Every extracted identity is explicit. A string records a strict one-to-one pair; `null` means unpaired. There are no per-test notes, statuses, or reviews. |

Go symbols combine the module-relative package and exported symbol: `agent.Agent`, `agent.Response.String`, or `workflow/inproc.ExecutionEnvironment.Run`. The module prefix is recorded once in `baseline.go_module`. One .NET declaration can reference several Go symbols. `go_symbols` is always an array; for assessed leaves it is empty only for `unmapped` or `intentional` declarations.

Notes are optional for `mapped` entries: the targets and example usually convey the correspondence without repeating it in prose. Keep a note only when it adds useful context. `adapted`, `partial`, `unmapped`, and `intentional` assessments still require a note explaining the adaptation, limitation, missing counterpart, or confirmed decision. Go-only assessments also require their explanation. Omitting a mapped note does not certify behavioral parity or establish current verification.

For non-property declarations, `go` is a minimal Go snippet showing the correspondence. It is an empty string only when no counterpart is recorded. Property mappings intentionally omit `go`: their `go_symbols` identify the field, accessor, or composed API without adding a contrived usage example. Examples contain no imports or synthetic outer function; conventional free identifiers such as `ctx`, `session`, and `logger` denote caller-supplied values. Reconciliation resolves and type-checks package-qualified APIs against the indexed Go API without executing them.

Extraction-added API placeholders have `unreviewed: true`, `status: unmapped`, empty `go_symbols`, and an empty `go` for non-properties. They have no `note`; the stored status is not an assessed gap. Remove `unreviewed` only when recording an actual assessment, preserving declaration keys and extraction markers. Placeholders do not claim an assessment against the original baseline. Existing assessments outside the extracted assembly scope remain valid catalog entries without being treated as current extracted declarations.

The catalog deliberately assumes that namespace/type/member keys are the declaration labels and that entries in selected assemblies/namespaces are current unless marked `unavailable`. Method return types appear once in their keys, not in separate fields. It does not duplicate those labels or full parameter/return/value metadata, generic metadata, base types, or nullability vectors in a `declaration` object. Full metadata remains in transient schema-1 extractor output and is used to resolve refreshes; the reporting projection uses the stored labels directly and is not a replayable raw extraction.

### Symbol identity

- Use the declaring .NET namespace, not the project/assembly name. For example, `CosmosChatHistoryProvider` is declared in `Microsoft.Agents.AI`.
- Keep overloads separate. Use source-like generics and `ref`/`out`/`in`, without parameter names, defaults, `this`, or `params`. A constructor key is `TypeName(ParameterTypes)`. Reference-nullability annotations do not distinguish overloads; nullable value types still do. New extracted labels omit reference-nullability annotations; existing assessment keys retain their spelling in reports.
- Every API method key includes ` -> ReturnType`, including `void`, async wrappers such as `Task<T>` and `ValueTask<T>`, and method-generic returns. Do not unwrap async results. Constructor keys, property keys, and test names remain unchanged.
- Refreshes resolve labels against full canonical CLR metadata, including positional generic binding and nullable value-type distinctions. Ambiguous matches fail rather than selecting an arbitrary same-named type or overload; a canonical `identity` override can preserve a known exceptional identity. Reconciliation reads the stored labels exactly without maintaining or reconstructing a second reflection inventory.
- Short parameter and return-type names are preferred; nested names retain their declaring type. Ambiguous type names require a namespace suffix. Unsupported signature spellings remain canonical or unresolved, never silently simplified.
- Put inherited members under their actual declaring type rather than duplicating them under every subclass. One mapping can list several Go counterparts where Go declares methods directly instead of inheriting them.
- Member names and their containing type form a stable fully qualified symbol for reports and issue references. Do not infer coverage from matching names alone.

## Statuses

These meanings apply to assessed leaves, not extraction-added placeholders.

| Status | Meaning |
| --- | --- |
| `mapped` | A direct API counterpart was identified. Ordinary naming, property/accessor, or language-type differences may remain. This is not a behavioral-parity claim. |
| `adapted` | The counterpart uses Go composition or a different API shape, such as middleware, options, function fields, or a factory. This alone is not a gap. |
| `partial` | A counterpart exists but the note identifies a specific unsupported aspect of the recorded declaration. |
| `unmapped` | After assessment, no Go counterpart was located for the upstream declaration. Scope, stability, and applicability still need review. |
| `intentional` | A confirmed decision explains why no direct counterpart is planned. The note must identify that decision; do not infer intent from absence or inherit it from an old comparison. |

Status belongs to each mapping leaf. A mapped type can contain unmapped members, and a member-only type container makes no claim about a whole-type counterpart. Language-specific machinery such as DI, class inheritance, or serializer options is not automatically a defect.

### Go-only assessments

The top-level `go_only` object uses the same canonical Go symbol names as `go_symbols`. Each value contains only a concrete `note` explaining why the API is Go-specific. There is no status, example, review reference, or inherited member coverage. An empty object is valid; missing mappings are never classified automatically.

Use a normal `adapted` mapping when Go composition or a different API shape implements a .NET contract. Missing inventory coverage or unresolved evidence is not enough for `go_only`. A symbol cannot appear in both `go_only` and a .NET leaf's `go_symbols`. If a counterpart is later verified, remove the Go-only entry when adding the normal mapping. Remove entries for deleted Go APIs with source evidence in the PR discussion and Git history. Re-review changed APIs or new evidence, without renewing unchanged assessments.

## Query the Mapping

Run from the repository root with `go -C _catalog run ./cmd/symbolmap <subcommand> [flags]`. The [symbolmap command](../_catalog/cmd/symbolmap/main.go) returns JSON by default and limits row-producing responses to 20 matches per page. Use filters before requesting more rows. `mappings`, `gaps`, and `go-only` only read the catalog; `go` indexes one Go checkout and `changes` compares two, without loading the catalog; `reconcile` reads declarations and assessments from the same catalog and indexes Go. Go indexing invokes the local toolchain and reads Git metadata. These subcommands never write the catalog or access the network themselves.

Catalog tooling lives in the independent [nested module](../_catalog/go.mod), `github.com/microsoft/agent-framework-go/_catalog`; the SDK does not require or replace it. The nested module boundary excludes the catalog and tooling from the root SDK module ZIP; the underscore prefix alone only skips package discovery. `-C` changes the process working directory to that module, so `-file` defaults to the [catalog](../_catalog/dotnet-go-sdk-symbol-mapping.json) basename and `-go-root` to `..` (the SDK parent). Explicit path flags are relative to that directory or can be absolute; use `-file <catalog>` for a different catalog.

Agents can start with the short [symbol lookup skill](../.github/skills/dotnet-symbols/SKILL.md); this guide remains the detailed reference.

| Question | Command |
| --- | --- |
| Show the first page of declarations and recorded mappings | `go -C _catalog run ./cmd/symbolmap mappings` |
| Summarize all explicit entries, including unreviewed placeholders | `go -C _catalog run ./cmd/symbolmap mappings -summary` |
| Summarize assessed symbols only | `go -C _catalog run ./cmd/symbolmap mappings -assessed -summary` |
| Inspect a type's properties | `go -C _catalog run ./cmd/symbolmap mappings -type AgentResponse -kind property` |
| Restrict a namespace | `go -C _catalog run ./cmd/symbolmap mappings -namespace Compaction` |
| Find the recorded Go counterpart of a .NET symbol | `go -C _catalog run ./cmd/symbolmap mappings -assessed -symbol AIAgent.RunAsync` |
| Find assessed .NET symbols that use a Go counterpart | `go -C _catalog run ./cmd/symbolmap mappings -assessed -symbol agent.Session.Get` |
| Read reviewed Go-specific APIs | `go -C _catalog run ./cmd/symbolmap go-only -symbol agent. -limit 5` |
| List workflow gap candidates | `go -C _catalog run ./cmd/symbolmap gaps -area workflows` |
| List assessed declarations with no located counterpart | `go -C _catalog run ./cmd/symbolmap mappings -assessed -status unmapped` |
| Inspect a type's mappings as JSON | `go -C _catalog run ./cmd/symbolmap mappings -type AgentRunOptions` |

Flags follow the subcommand. Use `-help` on a subcommand for its supported flags. `mappings -summary` reports counts instead of declaration rows. Mapping filters intersect: `-area`, `-kind`, and `-status` use the documented values; `-namespace`, `-type`, and `-symbol` are case-insensitive substring searches. `-symbol` accepts short or namespace-qualified .NET names and combined Go names. `mappings`, `gaps`, and `reconcile` accept `-assessed` to exclude unreviewed entries. `gaps` always selects only assessed `partial` and `unmapped` leaves, even with `-assessed=false`; extraction placeholders do not expand the automation backlog.

JSON is the default for every subcommand; use `-json=false` for human-readable text. Row-producing JSON responses (`mappings`, `gaps`, `go-only`, `go`, `changes`, and `reconcile` without `-summary`) include `page` with `total` matches after filtering, `offset`, `limit`, and `returned`. When `page.next_offset` is present, repeat the same filters with `-offset` set to that value; its absence means there are no more matches. `-limit` defaults to 20; use `-limit=0` explicitly to export every filtered match. `-limit` and `-offset` cannot be combined with `-summary`, whose counts always cover the entire filtered selection. Text reports show the same page information.

Mapping output includes the original baseline and a flat `mappings` array with short `dotnet` labels, their `namespace`/`assembly`, and assessments or `unreviewed` placeholders. The hierarchy remains the single maintained source. Summary counts include explicit leaves, not grouping-only type containers, and deduplicate Go targets shared by several .NET declarations. The optional `unreviewed_symbols` count identifies placeholders; default `unmapped` totals include their storage status. Use `mappings -assessed -summary` for assessed-only counts. Counts are not a parity percentage or a count of implementation tasks.

The `go-only` view returns the original baseline and a paged `go_only` object. It supports `-file` and `-symbol`, not .NET filters. `mappings -summary` reports the separate, unfiltered `go_only_symbols` count when nonzero; these symbols do not enter .NET mapping/status/gap counts or the mapped `go_symbols` count.

## Extract the .NET Declaration Inventory

The [mapping catalog](../_catalog/dotnet-go-sdk-symbol-mapping.json) contains public declarations from the three core MAF 1.22.0 packages at `net8.0` and 6,750 test method declarations from 45 upstream test assemblies. API package metadata retain version provenance under `dotnet`; test assemblies record source commits, target frameworks, and hashes under `dotnet.tests`. Public APIs from every provider integration or external dependency are not included; existing out-of-scope assessments are retained without extracted declaration metadata.

The separate [dotnetsymbols command](../_catalog/cmd/dotnetsymbols/README.md) extracts an inventory from compiled .NET assemblies using `go-winmd`. By default, `go -C _catalog run ./cmd/dotnetsymbols` resolves the latest stable MAF release and downloads its prebuilt core NuGet packages, without Git or a .NET SDK. Pin a version with `-release 1.22.0` for reproducible reruns. Select additional packages with `-package` and an exact target framework with `-framework`. Package versions, hashes, source metadata, and assembly identities are recorded; the release is not assumed to match this catalog's commit baseline.

For unpublished source, build the relevant upstream library projects separately and pass their reference or implementation assemblies with `-assembly`. Both modes include types, declared members, overloads, events, generic constraints, and experimental/compiler-generated markers without executing assembly code. Extraction omits metadata not used for reconciliation, such as parameter names/defaults, constant values, raw attribute lists and unused API flags. No per-symbol paths or line numbers are stored.

Normal extraction emits deterministic, transient raw schema-1 JSON with `identity_format: ecma335-v1` and full matching metadata to stdout without modifying the catalog. There is no separately maintained inventory. Explicit `-update-mapping` merges successful extraction into an existing compact schema-0 catalog in place, without JSON stdout. `-input` accepts only cached raw schema-1 extraction JSON offline; without an update flag, stdout remains raw schema 1. See the extractor guide for flag exclusions, merge safeguards, selection rules, and parser limitations.

| Purpose | Command |
| --- | --- |
| Read-only pinned API extraction smoke check | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0` |
| Refresh the selected API scope in the existing catalog | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0 -update-mapping dotnet-go-sdk-symbol-mapping.json` |
| Refresh from a cached extraction offline | `go -C _catalog run ./cmd/dotnetsymbols -input <cached-extraction> -update-mapping dotnet-go-sdk-symbol-mapping.json` |

Refreshes update only supplied assembly scopes and preserve assessment values, pairs, Go-only notes, and the original baseline. They complete existing method return labels using the existing generic parameter names; constructor/property keys and test names remain unchanged. API-only refreshes leave tests unchanged. New APIs become unreviewed placeholders, never automatic counterpart matches or assessed gaps. Metadata selection changes and refreshed packages that drop or replace recorded assemblies require separate review and fail without overwriting the catalog. Refreshing a package-owned assembly requires its recorded owning packages in the input so package and assembly provenance are updated together; local extraction can refresh only unowned assembly scopes. Refresh requires exclusive access to the catalog; concurrent updates or editor writes are not supported. The extractor is Go-only and never builds .NET projects; do not redirect its transient stdout onto the maintained catalog.

## Reconcile the Inventories

Reconciliation reads declarations through an in-memory projection of the single catalog. Reports retain their `inventory` metadata fields, extraction hash, and package/assembly provenance; no duplicate declaration inventory is maintained.

The Go index reads exported types, functions, methods, fields, constants, and variables from the SDK packages. It follows pointer receivers, aliases, and unambiguous promoted members. It excludes main/internal packages and tests, and never executes package initializers. Symbols remain combined names such as `agent.Agent.ID`; no per-symbol paths or lines are recorded.

| Question | Command |
| --- | --- |
| Summarize the current Go public API | `go -C _catalog run ./cmd/symbolmap go -summary` |
| Inspect actual Go signatures | `go -C _catalog run ./cmd/symbolmap go -symbol agent.Session` |
| Export the complete Go index | `go -C _catalog run ./cmd/symbolmap go -limit=0` |
| Summarize declaration reconciliation | `go -C _catalog run ./cmd/symbolmap reconcile -summary` |
| Summarize assessed API outcomes only | `go -C _catalog run ./cmd/symbolmap reconcile -assessed -summary` |
| List the review queue for a type | `go -C _catalog run ./cmd/symbolmap reconcile -state unreviewed -type AgentSession` |
| Find broken or ambiguous .NET references | `go -C _catalog run ./cmd/symbolmap reconcile -state needs-reconciliation` |
| Find invalid Go targets | `go -C _catalog run ./cmd/symbolmap reconcile -state invalid-go-target` |
| Validate all recorded in-scope references | `go -C _catalog run ./cmd/symbolmap reconcile -summary -check` |
| Get the next review-queue page with provenance | `go -C _catalog run ./cmd/symbolmap reconcile -state unreviewed -offset 20` |

Reconciliation states are separate from mapping statuses:

| State | Meaning |
| --- | --- |
| `linked` | The assessment references one current extracted declaration, and all recorded Go targets exist. An assessed empty target list is valid; linked does not mean implemented. |
| `unreviewed` | An extracted declaration has no uniquely linked assessment. Reconciliation omits its status, even though its catalog placeholder stores `unmapped`. |
| `needs-reconciliation` | A declaring type/member is absent, ambiguous, or claimed by conflicting assessments. Inspect selection, visibility, source revisions, and spelling; do not assume removal. |
| `invalid-go-target` | A recorded target is absent from the selected Go API. |
| `outside-inventory-scope` | The recorded assembly or namespace was excluded from extraction. These rows are retained, not counted as removed APIs. |
| `go-outside-scope` | A target package was not indexed under an explicit `-go-package` selection. Its existence remains unverified. |

Unresolved assessments never consume an unreviewed declaration. The queue includes type declarations even when only members have assessments. Experimental markers and compiler-generated/delegate machinery are identified separately. `suggested_go` contains name-based candidates, preferably under an existing type mapping; candidates are not assessments and can be unrelated APIs.

`reconcile -file` selects a different catalog; there is no `-inventory` flag. `go` and `reconcile` accept `-go-root` to select a Go checkout. The standalone `go` subcommand discovers its module from that checkout, without reading the mapping catalog; `reconcile` also checks that the module matches the catalog baseline. Repeat `-go-package` to replace the default agent/message/tool/provider/workflow package set, and use `-tags` for an explicit build-tag selection. The report records the effective platform, architecture, CGO setting, Go version, tags, Git commit and dirty state. It describes one build configuration, not the union of all platforms. Missing dependencies or invalid packages fail indexing rather than producing misleading missing-symbol results.

Indexing requires the local Go toolchain and cached SDK dependencies, prepared with `go mod download` from the repository root. Preload the separate tooling module with `go -C _catalog mod download`. The indexer disables module/toolchain downloads and uses read-only module loading. The outer `go run` itself follows the caller's ordinary Go environment. Configured custom tool/cache programs are rejected by the indexer. No .NET SDK is used.

`-check` checks the entire report, even when `-assessed`, other filters, or pagination hide failing rows. It fails on unresolved .NET references or invalid Go targets, including invalid Go targets on out-of-scope .NET rows. Being unreviewed or outside the .NET inventory scope alone does not fail this reference check. The report is still emitted before the nonzero exit status. `-summary` omits rows but retains selection provenance; counts apply to the entire filtered selection, not just one page, while `inventory_declarations` records the complete projected declaration input size.

When Go-only assessments exist, reconciliation also includes an unfiltered `go_only` summary with `assessed_symbols`, `present_symbols`, and any `invalid_go_targets` or `unindexed_go_targets`. `-check` fails on invalid Go-only targets even if .NET filters hide all rows. Explicitly unindexed packages remain unknown, not invalid. Presence validates the Go declaration, not the assessment that no .NET counterpart exists.

The original `baseline` remains historical provenance, independently of the latest `dotnet.sha256`, newer assessments, or catalog formatting. For assessed leaves, `review_source_changed` and `review_go_changed` compare the current .NET extraction and Go commits only with that original baseline, not with a leaf's latest inspection. These flags indicate revision differences, not behavior or when an entry was last verified. A dirty Go checkout also prevents treating HEAD alone as an exact snapshot. Matching identities does not certify old assessments against a newer release.

## Map Test Declarations

The `tests` section in the catalog explicitly records every extracted .NET test identity, with a Go test string for a strict one-to-one pair or `null` when unpaired. It is separate from API `namespaces` and does not change API mapping counts or statuses. Only nonnull strings count as pairs; absent or unpaired entries do not imply that Go has no equivalent tests.

Extract test declarations with `dotnetsymbols -test-assembly` and explicitly merge them with `-update-mapping` as described in the [extractor guide](../_catalog/cmd/dotnetsymbols/README.md#optional-test-declarations). The saved catalog includes test assemblies built for `net10.0` at the core 1.22.0 source revision; its API package selection remains `net8.0`. With extraction metadata present, catalog validation requires `dotnet.tests` metadata for every stored test assembly, including assemblies containing only unpaired declarations. A test report rejects missing metadata rather than reporting zero tests. API and test assembly hashes must be valid SHA-256 values. Test assemblies must be supplied from the intended pinned build, since core NuGet packages do not contain them.

Catalog test keys follow **assembly → fully qualified CLR declaring type → test name**, without a separate namespace level. Each value is one module-relative Go test name, for example `"RunAsync": "agent.TestRun"`, or `null`, on a single line. Root-package tests use `..TestName`. There are no per-test objects, arrays, notes, statuses, or review records. Copy the .NET name without parentheses or a return type; declaring types retain their nested-type `+` and generic arity suffixes. Global types use their unqualified name. Transient extraction arrays are merged into these keys; `dotnet.tests.assemblies` retains only build metadata, not another name list.

Test mappings perform an exact identity join. Each .NET test has at most one Go target, and each Go target can be paired only once across all assemblies and types. Duplicate names within one type are rejected during extraction rather than treated as one test. Record only direct ports of the primary setup, action/entrypoint, assertions, and case data, with only minor language-required differences. Similar coverage or an analogous added subtest is not enough. Partial overlap or coverage split across functions must remain unpaired rather than forcing an arbitrary pair. Keep unpaired .NET identities as `null`. Use existing Go test names, not CLR-aligned renames. Subtests and table rows are not separate identities.

Within refreshed test assemblies, removed null entries are dropped. Removed paired entries retain their strings and are listed under `dotnet.tests.unavailable[assembly][CLR type]` for `needs-reconciliation`; they are not projected as current declarations. Unrefreshed assemblies and existing pairs remain unchanged.

| Question | Command |
| --- | --- |
| Discover Go test functions without compiling or running them | `go -C _catalog run ./cmd/symbolmap go-tests -symbol agent.Test -limit 5` |
| Summarize test pairs and unpaired declarations | `go -C _catalog run ./cmd/symbolmap tests -summary` |
| Find unpaired declarations in a test assembly | `go -C _catalog run ./cmd/symbolmap tests -assembly Microsoft.Agents.AI.UnitTests -state unreviewed -limit 5` |
| Find the .NET declaration paired with a Go test | `go -C _catalog run ./cmd/symbolmap tests -symbol agent.TestRun` |
| Check every recorded test reference | `go -C _catalog run ./cmd/symbolmap tests -summary -check` |

`tests` accepts `-file` for the single catalog and `-go-root`, not `-inventory`. Filters for `-assembly`, `-namespace`, `-type`, and `-symbol` are case-insensitive substrings; `-state` selects an exact value. There is no test `-status` or `-assessed` flag. JSON/text output, summaries, and pagination follow the API command conventions. Rows contain a single `go_test` when paired; `mapped_tests` counts only selected nonnull pairs before pagination. The states are `linked`, `unreviewed` for null declarations, `needs-reconciliation`, and `invalid-go-target`. Here `unreviewed` means unpaired, not a stored Go source-review classification. A linked row validates references, not behavioral parity or passing tests. `-check` validates the entire unfiltered report, including missing Go targets on dangling .NET rows. An unreviewed declaration does not fail this check or imply a missing Go test.

The catalog and test reports do not track or classify unpaired Go tests. `go-tests` remains a standalone discovery command, independent of the catalog; discovery does not create a review queue.

The Go test index statically parses all test-source build variants in the module, including internal and example packages. Its `tests` output is a sorted array of unique directory-qualified names, without signatures. It excludes nested modules, vendor/testdata directories, dot/underscore-prefixed names, and symlinks. It finds top-level `Test` entrypoints using `*testing.T`, including renamed/dot imports, but does not resolve type aliases, compile packages, enumerate subtests, run tests, or load dependencies. Internal/external test packages and alternate builds share their directory-qualified identity. Presence therefore means at least one declaration, not buildability, uniqueness within a build, or execution coverage.

Reports retain the test assemblies' source commits, target frameworks, and hashes independently of the API inputs, together with the current Go source metadata. They do not store per-test inspection records or calculate review-change flags. The .NET side describes compiled declarations in the supplied builds, while the Go side describes source entrypoints across build variants. Counts are not comparable runtime-case totals or a coverage percentage. Compare the relevant .NET and Go assertions before adding a pair.

## Compare Go API Changes

Use `go -C _catalog run ./cmd/symbolmap changes -old-root ../../old-checkout -go-root .. -limit=0` to compare two checkouts of the same Go module with the pinned [`golang.org/x/exp/apidiff`](https://pkg.go.dev/golang.org/x/exp/apidiff) package. Prepare both checkouts and their dependencies separately; the command neither checks out revisions nor downloads modules. It reuses the Go index's package selection and excludes main/internal packages and tests.

The JSON report contains `old` and `new` build/commit metadata, compatible and incompatible counts, and a paged `changes` array of `{message, compatible}` diagnostics. Both classifications are included: an added exported struct field is normally a compatible change. `-symbol` filters diagnostic text case-insensitively, `-summary` omits the individual changes, and `-json=false` prints a text report. Finding incompatible changes does not cause a nonzero exit; input/load failures do. Package additions and removals are reported too; inspect members of newly added types and packages separately.

The checkouts must have the same module path, platform, architecture, CGO setting, Go version, and build tags. `-go-package` and `-tags` apply to both. An incomplete load fails without a report. The comparison describes the net structural change between the two snapshots, not all intermediate commits or build configurations. Function bodies, documentation, defaults, and struct tags still need source-diff review. Diagnostic messages are review leads, not stable symbol identifiers or .NET mapping assessments.

## Derive and Maintain Work

1. Start with `go -C _catalog run ./cmd/symbolmap reconcile -state unreviewed` and inspect declarations one type at a time. Resolve any .NET candidates and inspect suggested Go APIs, their callers, documentation and tests. Record one-to-many Go compositions where appropriate; do not derive a status from name similarity.
2. Only after assessment, consider `partial` or `unmapped` declarations as work candidates. Check experimental gates and open/closed issues or PRs. Several declarations may belong to one task; deduplicate related constructors, options, and types before proposing work.
3. Confirm the missing observable behavior and choose a narrow change with behavioral tests. An API adaptation may already cover the use case; new public Go APIs are not required just to resemble .NET.
4. Update the specific type/member assessment and remove `unreviewed` when assessed. Preserve `dotnet` metadata, declaration keys, assembly/kind, and extraction markers. Add unlisted declarations only through an explicitly requested extractor update; ordinary assessment edits do not add or rename declaration keys. Preserve actual declaring ownership and record any concrete limitation without asserting all-member parity.
5. Run `go -C _catalog run ./cmd/symbolmap mappings -assessed -summary` and `go -C _catalog run ./cmd/symbolmap reconcile -summary -check`. The first validates schema, duplicate keys/symbols, overload formats and Go target syntax while reporting assessed-only counts; reconciliation additionally resolves .NET declarations and checks actual Go symbols. Command tests can be run with `go -C _catalog test ./cmd/symbolmap`. No provider or end-to-end runs are needed for a catalog-only edit.

Preserve the original baseline as historical provenance. Record newly inspected revisions, supporting source/test evidence, and any approved omission in the change's PR discussion and Git history, not in new catalog review records. Reformatting or grouping does not refresh verification, and inspecting a subset does not justify advancing the original baseline. The previous broad feature comparison remains available in Git history.

Existing links to this guide remain valid. The [API porting workflow](../.github/workflows/dotnet-port-api-nightly.md) selects assessed `partial`/`unmapped` gaps and rechecks them against current Go and the source revision recorded in the catalog's `dotnet` metadata, not upstream `main`. Both API and upstream-fix workflows read the single catalog without modifying it; API-port PRs name the addressed leaves. Weekly mapping maintenance records implemented counterparts after merge using published Go commits; release upgrades and explicit declaration refreshes remain separate maintainer work. Do not append another feature table, package checklist, or maintained count list here.

## Weekly Mapping Maintenance

The [weekly workflow](../.github/workflows/symbolmap-maintenance-weekly.md) runs weekly and supports manual dispatch. Each run checks every existing assessed mapping for staleness from recent Go work or preexisting inaccuracies; it does not rotate through a subset. Revision changes alone do not establish staleness.

Its draft PRs can change only assessment data in the catalog. The `dotnet` metadata, declaration keys, assembly/kind, and extraction markers are read-only references: the workflow does not refresh extraction, query newer .NET releases, or assess whether extraction is stale. Declaration maintenance remains manual. New assessments must be tied to recent Go implementations; the unrelated unreviewed backlog, SDK changes, and package expansion remain outside its scope. Open results do not prevent subsequent audits; duplicate corrections are skipped. A complete audit with no new substantiated correction reports a no-op; an unfinished audit reports itself as incomplete.

The workflow prepares the past-week SDK patch (including internal code and tests), API mappings selected with `mappings -assessed`, Go-only assessments, and an API comparison between HEAD and its last first-parent ancestor before the review window. The assessed snapshot excludes extraction placeholders without expanding the gap backlog. The API comparison baseline is a temporary detached worktree, removed after preparation. Behavioral review is required even when the API report is empty: internal-helper and dependency changes are traced to public contracts and existing assessments. Implementation PRs and relevant discussion can clarify intent, but source/tests at the inspected revisions establish behavior. Prior mapping/audit PRs are consulted only after independent discovery, for duplicate checks and maintainer feedback. Every API change needs a reviewed outcome, not a forced .NET mapping; confirmed Go-specific assessments can be recorded in `go_only`.

The main agent delegates fresh, bounded review batches to the embedded `symbolmap-reviewer`: recent APIs by Go declaring type, behavioral diffs by commit/file, then existing assessments by .NET namespace/type. At most two reviewers run concurrently. Each returns an outcome and pinned evidence for every assigned item; missing items and unresolved reads remain pending rather than counting as reviewed. Literal-name misses require broader declaring-type inspection, and throttled code search falls back to pinned file reads. The main agent reconciles the returned identities, checks duplicates, and makes catalog edits only after independent review. No persistent review ledger or custom publication gate is used.
