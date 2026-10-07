# .NET symbol extraction

`dotnetsymbols` reads managed assembly metadata with [go-winmd](https://github.com/microsoft/go-winmd) and normally writes transient raw schema-1 JSON to standard output without modifying the maintained [symbol catalog](../../dotnet-go-sdk-symbol-mapping.json). By default it downloads the latest stable MAF release; exact release pins and local assemblies are also supported. Optional test assemblies add test method declarations using the same metadata reader. Output is deterministic for the same selected package versions and assembly bytes. Explicit `-update-mapping` instead refreshes an existing compact schema-0 catalog in place, without JSON stdout. It does not load or execute .NET code, restore dependencies, build projects, or execute tests.

Run all examples from the repository root. `go -C _catalog` changes the process working directory to the independent [catalog module](../../go.mod), so relative path flags are resolved there; use absolute paths when needed.

## Extract a published release

Use NuGet packages rather than building the solution when a published release is sufficient. This mode needs only Go and HTTPS access to a public NuGet v3 feed—no upstream Git checkout or .NET SDK.

| Selection | Command |
| --- | --- |
| Latest stable core MAF release (default) | `go -C _catalog run ./cmd/dotnetsymbols` |
| Explicit latest stable release | `go -C _catalog run ./cmd/dotnetsymbols -release latest` |
| Pinned core MAF release | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0` |
| Same version with the .NET release-tag prefix | `go -C _catalog run ./cmd/dotnetsymbols -release dotnet-1.22.0` |
| Latest release, only the abstractions package | `go -C _catalog run ./cmd/dotnetsymbols -package Microsoft.Agents.AI.Abstractions` |
| Selected core and provider packages | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0 -package Microsoft.Agents.AI.Abstractions -package Microsoft.Agents.AI.OpenAI` |
| Latest release, a different target framework | `go -C _catalog run ./cmd/dotnetsymbols -framework net10.0` |
| Latest release from the public .NET package mirror | `go -C _catalog run ./cmd/dotnetsymbols -nuget-source https://pkgs.dev.azure.com/dnceng/public/_packaging/dotnet-public/nuget/v3/index.json` |

Without `-package`, the selected packages are `Microsoft.Agents.AI`, `Microsoft.Agents.AI.Abstractions`, and `Microsoft.Agents.AI.Workflows`. This is the core set, not every MAF integration. Repeating `-package` replaces that default set. Use `ID@version` when an explicitly selected package has a different version, such as a preview integration or a `Microsoft.Extensions.AI` dependency. Dependencies are not selected automatically.

Omitting `-release`, or passing `-release latest`, selects the highest stable version of `Microsoft.Agents.AI` advertised by the selected feed. Version components are compared numerically, prereleases are excluded, and the resolved version is used for every package without an explicit `ID@version` override. The version is resolved once per invocation, not independently for each package. If every package has an exact override, no latest-version lookup is needed. Missing packages at that release fail rather than falling back to older or prerelease versions.

Use an exact `-release` version for reproducible reruns; the output always records the actual package versions and hashes. The optional `dotnet-` prefix on a pinned version is removed without GitHub tag resolution. Version ranges and Git commits are not accepted. NuGet normalization removes leading numeric zeros, a zero fourth component, and `+build.metadata` for package addressing and identity checks; the manifest's original version is retained in the output. The catalog's original commit baseline remains unchanged.

The default framework is `net8.0`. The command selects the exact `ref/<framework>` asset group when present, otherwise the exact `lib/<framework>` group. There is no compatibility or version fallback. Missing versions, missing framework groups, packages with no DLLs, conflicting package versions, and unsupported assemblies fail without emitting a partial inventory. Available asset groups are reported when the requested framework is absent.

The default source is `https://api.nuget.org/v3/index.json`; `-nuget-source` accepts another public HTTPS NuGet v3 service index. The selected source is recorded, with no silent mirror fallback. On hosts unable to establish TLS with NuGet.org, the public .NET mirror can be selected explicitly as shown above. Authentication and credential-provider integration are not supported.

Packages and selected assemblies are read in memory, never unpacked to the working tree. Package scripts, build targets, and assembly code are not executed. Requests have a two-minute timeout; package downloads and individual assembly assets are limited to 128 MiB. Progress and errors go to standard error, keeping standard output JSON-only. Package and assembly hashes identify downloaded bytes; they are not a NuGet signature-verification or trust policy.

Supplying `-assembly` selects local mode instead of the default release lookup and makes no network requests. Explicit `-release`, `-package`, `-framework`, or `-nuget-source` flags cannot be combined with `-assembly`. Namespace and protected-member filters work in both modes.

## Extract local assemblies

1. Check out the .NET revision being inventoried and use the SDK required by that checkout. The core 1.22.0 source revision (`0c9944cc9f577d51277ac7c55dbc388b60a577af`) requires .NET SDK **10.0.401**; building it with an older SDK is not supported.
2. Build the relevant library projects in one configuration and target framework, rather than the full solution's tests and samples. Reference assemblies are preferred, but implementation assemblies work too. Keep all build output outside version control.
3. Pass explicit assembly files or narrowly scoped globs to the command. Include dependency assemblies such as `Microsoft.Extensions.AI.Abstractions` only when their public declarations belong in the inventory. Referencing a dependency does not cause it to be inventoried automatically.

Run from the Go repository root. Paths below are illustrative local build-output locations:

| Selection | Command |
| --- | --- |
| One assembly | `go -C _catalog run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll'` |
| Several selected assemblies | `go -C _catalog run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Workflows.dll'` |
| A dedicated reference-assembly directory | `go -C _catalog run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI*.dll'` |
| One namespace and its children | `go -C _catalog run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -namespace Microsoft.Agents.AI` |
| Public and externally inheritable API | `go -C _catalog run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -include-protected` |

Repeat `-namespace` to include multiple namespace trees. Without it, all namespaces in the selected assemblies are included. Do not assume every SDK extension uses a `Microsoft.Agents.AI` namespace: OpenAI and Azure client extensions are declared in their client namespaces.

Patterns use Go's `filepath.Glob` syntax, not recursive `**`. Unmatched patterns fail. Repeated identical assembly bytes are deduplicated; different inputs with the same assembly name fail, so accidentally mixing target frameworks or reference/implementation copies cannot silently merge API surfaces. Conflicting type definitions also fail. Output is buffered until every selected assembly has been processed successfully.

## Optional test declarations

Repeat `-test-assembly <file or glob>` to add a separate `tests` section alongside the transient API extraction. Both release and local API-assembly modes support it. Omitting the flag produces API-only extraction and leaves existing catalog tests unchanged during an update. API namespace and visibility filters do not filter test declarations.

Test assemblies are not included in the core NuGet packages. Obtain the implementation assemblies from a matching build artifact or build the selected upstream test projects separately at a pinned revision. Building is sufficient: **do not run the tests for inventory generation**. Select the projects needed for the intended scope, including integration/conformance test assemblies when relevant. The extractor does not check out or build source, invoke Git, or require a .NET SDK.

| Selection | Command |
| --- | --- |
| Core release APIs plus a test assembly | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0 -framework net8.0 -test-assembly 'C:/temp/agent-tests/Microsoft.Agents.AI.UnitTests.dll'` |
| Local API and selected test assemblies, without network access | `go -C _catalog run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -test-assembly 'C:/temp/agent-tests/*.dll'` |

Paths are illustrative. Use a directory containing only the chosen test assemblies, not every dependency DLL from a build output. Unmatched patterns, reference assemblies, assemblies with no supported test declarations, conflicting builds with the same assembly name, and duplicate test identities fail without a partial report. Repeated identical assembly bytes are deduplicated. Use normal stdout for a read-only extraction smoke check; add `-update-mapping` only for an explicitly requested refresh of the existing [catalog](../../dotnet-go-sdk-symbol-mapping.json).

### Test metadata and scope

The following describes transient raw schema-1 stdout. Catalog updates store identities in the top-level `tests` table and retain build metadata under `dotnet.tests`, without duplicating name lists.

- `tests.identity_format` is `test-name-v1`: assembly, CLR declaring type, and test method name. The existing metadata reader identifies tests without parsing C# or rendering their signatures.
- `tests.assemblies` is keyed by the manifest assembly name. Each entry records its source `commit`, target framework, SHA-256, and `types`. The commit is extracted from a full 40- or 64-digit hexadecimal suffix after `+` in the assembly informational version and normalized to lowercase; it is omitted when unavailable. Assembly version strings are not retained for tests. Test build provenance is separate from API package provenance and is never inferred from the current checkout or API release.
- `types` maps each namespace-qualified CLR declaring type to a sorted array of test names. Nested types retain `+` and type arities retain their metadata suffixes. Method signatures, parameters, return types, generic method arities, and attributes are omitted. Duplicate test names within a type, including overloads, fail rather than silently merging declarations.
- Supported discovery markers are `Xunit.FactAttribute`, `Xunit.TheoryAttribute`, `xRetry.v3.RetryFactAttribute`, and `xRetry.v3.RetryTheoryAttribute`; they are not stored in the inventory. Unknown attribute names ending in `Fact` or `Theory` fail explicitly. Other frameworks, custom discoverers, and arbitrary derived test attributes are not automatically discovered.
- Each entry is **one attributed compiled method declaration**, not an executed test case. Skipped methods and declarations on abstract/base types remain present; inherited copies are not manufactured. Nonpublic declaring types are included. Attribute arguments, skip conditions, theory data providers, assembly code, and tests are never executed.
- The scope is the supplied builds, not every source configuration. Compilation resolves aliases, partial declarations, generated code, project includes, and conditional compilation. Excluded branches are absent. Inspect alternate builds of the same assembly in separate transient extractions; a catalog refresh selects one current build per assembly. Do not describe one target framework's extraction as all configurations or count theory methods as individual data rows.

See [test mapping and reconciliation](../../../docs/dotnet-go-sdk-feature-comparison.md#map-test-declarations) for recording reviewed Go counterparts.

### Current snapshot

The maintained [catalog](../../dotnet-go-sdk-symbol-mapping.json) retains the three core MAF 1.22.0 API packages at `net8.0` from the public .NET package mirror. Its test section contains **6,750 method declarations from 45 assemblies**, built from `0c9944cc9f577d51277ac7c55dbc388b60a577af` using SDK **10.0.401**, `Release`, and `net10.0`. This is one compiled target configuration, not runtime theory cases or every target framework. Reproducing this scope requires the same pinned API inputs and all selected test assemblies; an API-only refresh does not regenerate the test section.

All 48 test-bearing projects under the pinned test tree were built without running tests. `CopilotStudio.IntegrationTests`, `OpenAIChatCompletion.IntegrationTests`, and `OpenAIResponse.IntegrationTests` contain only inherited test methods; those declarations appear once in `AgentConformance.IntegrationTests`. The separate Foundry integration-test container host has no test declarations and was excluded.

Builds used `--artifacts-path` outside the checkout, `ContinuousIntegrationBuild=true`, and `SourceRevisionId` set to the verified checkout commit. `GITHUB_ACTIONS=true` prevents the upstream automatic formatting target, and `CopilotSkipCliDownload=true` omits the optional Copilot executable without changing compiled test source. The official NuGet v2 feed (`https://www.nuget.org/api/v2/`) supplied build dependencies unavailable from the public mirror; no source or dependency versions were changed. Assembly hashes record the actual build bytes. Test pairs and null unpaired identities live in the same catalog; extraction does not establish correspondence or advance the original API assessment baseline.

## Refresh the maintained catalog

Normal extraction is read-only with respect to the catalog. `-update-mapping <existing catalog>` explicitly selects an in-place update instead of stdout JSON; the destination must already be a regular file. The [shared catalog package](../internal/symbolcatalog/model.go) defines the model and validation used by both `dotnetsymbols` and `symbolmap`.

| Purpose | Command |
| --- | --- |
| Read-only pinned API extraction smoke check | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0 -framework net8.0` |
| Validate cached raw extraction and emit raw schema-1 JSON, offline | `go -C _catalog run ./cmd/dotnetsymbols -input <cached-extraction>` |
| Refresh the selected release API scope, preserving existing tests | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0 -update-mapping dotnet-go-sdk-symbol-mapping.json` |
| Refresh APIs and a selected test assembly | `go -C _catalog run ./cmd/dotnetsymbols -release 1.22.0 -test-assembly 'C:/temp/agent-tests/Microsoft.Agents.AI.UnitTests.dll' -update-mapping dotnet-go-sdk-symbol-mapping.json` |
| Merge validated cached extraction into the catalog, offline | `go -C _catalog run ./cmd/dotnetsymbols -input <cached-extraction> -update-mapping dotnet-go-sdk-symbol-mapping.json` |

`-input` reads only cached raw schema-1 extraction JSON without release lookup, network access, or assembly reads. The compact schema-0 catalog is not a raw interchange input. It cannot be combined with any explicitly supplied extraction, source, or scope flag: `-assembly`, `-test-assembly`, `-release`, `-package`, `-framework`, `-nuget-source`, `-namespace`, or `-include-protected`, even when the supplied value equals a default. Without `-update-mapping`, it emits normalized raw schema-1 interchange to stdout.

Updates preserve the catalog's schema 0, original `baseline`, and note-only `go_only` assessments:

- Only supplied API and test assembly scopes are refreshed. Their input metadata and canonical declarations are updated; unrefreshed scopes remain unchanged. API-only updates leave all test identities, pairs, and metadata unchanged.
- Existing assessment values and test pairs are preserved. Missing method return labels are completed using the generic names already in the existing labels; constructor keys, property keys, and test names remain unchanged. Every new API declaration gets an unreviewed placeholder: `unreviewed: true`, `status: unmapped`, empty `go_symbols`, an empty `go` for non-properties, and no `note`. Placeholders do not claim an assessment against the baseline and are not assessed gaps. New test identities are `null`; only nonnull Go test strings count as strict one-to-one pairs.
- Removed unreviewed APIs and null tests in refreshed scopes are dropped. Removed assessed APIs remain with `unavailable: true` for reconciliation. Removed paired tests retain their strings and are marked under `dotnet.tests.unavailable[assembly][CLR type]`, so reconciliation does not mistake them for current declarations.
- Changed namespace/protected-member selection fails without overwriting the catalog; scope changes require separate review. A refreshed package that drops or replaces a previously recorded assembly is also rejected, rather than retaining its old declarations as current. Packages not selected for refresh remain unchanged.
- Refreshing a package-owned assembly requires all its recorded owning packages in the input, with their matching package and assembly provenance. Local `-assembly` extraction has no package metadata and can refresh only unowned assembly scopes; use release extraction or cached package extraction for package-owned assemblies. A different selected package cannot overwrite an unselected package's assembly.
- API and test assembly hashes must be valid SHA-256 values. When extraction metadata exists, every stored test assembly must have corresponding `dotnet.tests` metadata, including assemblies containing only null pairs. Assembly-ownership conflicts, ambiguous identities, colliding labels, duplicate JSON keys or Go test targets, and invalid catalogs fail closed rather than choosing or overwriting an assessment.

All extraction inputs are processed before catalog validation, merging, and writing. Refresh requires exclusive access to the catalog: do not run another update or edit the file until it completes. No file lock is used. The merged result is validated before replacement. A temporary file is written, the destination is re-read, and detected content changes are refused as a best-effort safeguard, not an atomic compare-and-swap or concurrent-edit guarantee. Replacement uses `os.Rename`; this does not promise OS-level atomic replacement on Windows. Extraction never chooses feature matches, changes assessment statuses, or advances the original baseline automatically.

Do not redirect transient stdout onto the maintained catalog: raw schema-1 extraction is not the compact schema-0 catalog or a merge of its existing assessments. Redirection can truncate the destination on failure. Use `-update-mapping` for an explicitly requested refresh, then inspect the catalog and validate references as described in the [mapping guide](../../../docs/dotnet-go-sdk-feature-comparison.md#derive-and-maintain-work).

## Generated data

Normal stdout, including `-input` without an update flag, is transient raw schema-1 interchange with `identity_format: ecma335-v1` and full matching metadata, not the compact maintained catalog. It makes no automatic Go counterpart assessments:

- `selection`: namespace filters and protected-member policy.
- `packages`: present in release mode, keyed by package ID. Records exact version, source feed, download URL, package SHA-256, selected framework/asset group, and included assembly names. Repository URL and commit are retained when provided by the package manifest; a missing commit is not inferred from the version.
- `assemblies`: assembly version, informational version, target framework, SHA-256 of the input, reference-assembly marker, and unresolved type forwarders. Informational versions can carry a source revision even when the package manifest does not. For local builds, retain the source checkout revision separately; the extractor does not infer it from the current working directory.
- `types`: namespace-qualified CLR type names, declaring assembly, type kind, base type, generic parameter names/positions/constraints, and declared constructors, methods, properties, events, fields, and constants.
- `tests`: optional arrays of test names grouped by declaring type, with independent assembly build metadata and `test-name-v1` identities. Test methods do not enter API declaration counts.

The maintained [catalog](../../dotnet-go-sdk-symbol-mapping.json) embeds extraction schema/version, identity format, raw extraction snapshot `sha256`, selection, packages, and assemblies under `dotnet`. Namespace/type/member keys supply declaration labels; every API method key includes ` -> ReturnType`, including `void`, declared async wrappers, and method-generic returns, without unwrapping them. Constructor keys, property keys, and test names are unchanged. `class` is the default type kind, and selected assembly/namespace entries are current unless marked `unavailable`. Only nondefault `kind`, experimental/generated markers, and exceptional canonical `identity` overrides are stored beside assessments. There are no `declaration` objects repeating signatures or full parameter/return/value metadata, base types, generic metadata, or nullability vectors. Full matching metadata is used during refresh, not retained twice. Reporting reads exact stored labels rather than reconstructing raw reflection output. The catalog stores no named review records, per-leaf review references, or Go-test review queue; `go_only` values contain only `note`. The original baseline remains historical; evidence of later inspections belongs in PR discussion and Git history.

The catalog and its tooling remain tracked in Git as development assets in the independent module `github.com/microsoft/agent-framework-go/_catalog`. Its nested [module boundary](../../go.mod) excludes both from the root SDK module ZIP; the underscore prefix alone only skips package discovery, not ZIP content. The SDK does not require or replace this module. With `go -C _catalog`, catalog-reading `symbolmap` commands default to the catalog basename and `-go-root ..` selects the SDK parent; use an explicit `-file` for a different catalog.

All test identities live only under `tests[assembly][full CLR type][name]` as strings or nulls. `dotnet.tests` holds `identity_format`, assembly metadata without name lists, and optional `unavailable` removed-pair markers. No duplicate declaration inventory is maintained.

Extraction retains only declaration identities, matching metadata, compact markers, and input provenance. Parameter names/defaults, constant values, raw attribute-name lists, nullable contexts, interface lists, and unused API flags/accessor details are omitted. Empty attribute objects and empty parameter lists are omitted as well. This is not a complete API contract; inspect pinned source for defaults, mutability, lifecycle, and behavioral reviews.

There are no per-symbol paths or line numbers. Inherited members are not repeated under derived types. Visibility is applied during selection, not repeated in the JSON: nested types require visibility through the entire enclosing chain, while properties/events use accessor visibility. By default only public API is selected; `-include-protected` adds protected and protected-internal API, not private-protected or internal declarations.

Ordinary accessor methods are not duplicated in the method list; operators and associated helper methods remain methods. Public compiler-generated record members remain present, with a compact `compiler_generated` marker. Enum constant names/types are included but their runtime `value__` storage field is excluded.

### Stable API metadata signatures

Raw API extraction deliberately uses metadata spelling. The catalog keeps short source-like labels, falling back to canonical spellings for unsupported or ambiguous signatures; canonical `identity` overrides are recorded only when the surrounding label does not uniquely resolve:

| API | Example identity |
| --- | --- |
| Generic type | ``Example.Agent`1`` |
| Nested generic type | ``Example.Agent`1+State`1`` |
| Constructed generic type | ``System.Collections.Generic.List`1<System.String>`` |
| Generic method | ```Run``1(!0,!!0) -> !!0``` |
| Constructor | `.ctor(System.String)` |
| Indexer | `Item(System.Int32) -> System.String` |
| By-reference parameter | `System.Int32&`, with `ref`/`out`/`in` also recorded on the parameter |
| Array | `System.String[]` or rectangular `System.Int32[0:,0:]` |

In raw schema-1 extraction, type parameters use `!n`; method parameters use `!!n`. Return types remain in method/property keys so conversion operators and metadata-only overloads cannot collide. VARARG methods include a trailing `...` parameter marker. Array dimensions preserve lower-bound/size pairs, including omitted values. Custom modifiers and supported function-pointer calling conventions are retained. Duplicate canonical identities fail instead of overwriting a declaration. Names do not include assembly qualification in signatures; conflicting declarations, signatures, or same-name type references from different assembly scopes are rejected rather than merged.

Reference nullability does not change CLR overload identity. `nullable_flags` are retained on parameters/returns and property/event/field types where the reconciler uses them to distinguish nullable-reference spelling from nullable value types. They are not expanded into complete C# `?` spelling. Generic parameter positions and flags remain necessary for positional matching and value-type constraints. Attributes retain only these nullability flags, declared experimental diagnostic IDs, and compiler-generated markers.

### Boundaries

- This is **declaration extraction**, not Go counterpart discovery or behavioral verification. An explicit catalog update adds placeholders and declaration metadata, not automatic assessments.
- The catalog retains namespace groups and short source-like signatures with only exceptional identity overrides. Refresh uses full CLR metadata and rejects ambiguous matches; reconciliation reads the recorded labels and an index of the actual Go API without changing assessments. Unreviewed declarations are not automatically gaps. Existing assessments outside the three core assembly scopes are not projected as current extracted declarations and do not imply a missing feature. API scope remains incomplete. See the [mapping and reconciliation guide](../../../docs/dotnet-go-sdk-feature-comparison.md#reconcile-the-inventories).
- The current metadata reader requires compressed `#~` tables. It rejects some CLR signatures, including ref-return property signatures and modern unmanaged function pointers. For example, extracting `LinkedListNode<T>.ValueRef` from a runtime reference assembly fails explicitly. Unsupported selected signatures stop the inventory rather than being skipped.
- Multi-module assemblies are unsupported. Forwarded types are listed separately and are not loaded from their target assemblies.
- Obsolete annotations, arbitrary attribute arguments, accessor-only attributes, generic-parameter attributes, and constant values are not inventoried. Static-readonly values are not evaluated.
- Metadata does not reconstruct source aliases, tuple element spelling, `dynamic`, or runtime defaults computed by code. Use Roslyn if source-faithful C# display becomes a requirement.

Regenerating or reformatting an extraction snapshot changes its byte hash. `dotnet.sha256` records the raw extraction snapshot used for the latest refresh, not the catalog's current file hash. An extraction or formatting change does not advance the original historical baseline or date all current assessments. Automated `review_source_changed` and `review_go_changed` flags compare revisions only with that original baseline, not with the latest per-leaf inspection.

## Requirements

The extractor itself requires only Go. Preload its standalone tooling dependencies with `go -C _catalog mod download`. Release mode also requires HTTPS access to the selected NuGet feed; local mode and optional test extraction require already-built assemblies. Cached `-input` mode is offline. No mode invokes a .NET SDK. Build upstream projects separately when unpublished assembly metadata or test declarations must be inspected. No .NET source files, fixture projects, or assembly binaries are included with this command; Go unit tests construct small managed PE metadata fixtures in memory. Core declarations, test identities, and reviewed counterparts are maintained together in the [single catalog](../../dotnet-go-sdk-symbol-mapping.json), not a second generated documentation file.
