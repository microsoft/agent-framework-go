# .NET symbol extraction

`dotnetsymbols` reads managed assembly metadata with [go-winmd](https://github.com/microsoft/go-winmd) and writes a JSON inventory to standard output. By default it downloads the latest stable MAF release; exact release pins and local assemblies are also supported. Output is deterministic for the same selected package versions and assembly bytes. It does not load or execute .NET code, restore dependencies, build projects, or modify the reviewed [symbol mapping](../../docs/dotnet-go-sdk-symbol-mapping.json).

## Extract a published release

Use NuGet packages rather than building the solution when a published release is sufficient. This mode needs only Go and HTTPS access to a public NuGet v3 feed—no Git checkout or .NET SDK.

| Selection | Command |
| --- | --- |
| Latest stable core MAF release (default) | `go run ./cmd/dotnetsymbols` |
| Explicit latest stable release | `go run ./cmd/dotnetsymbols -release latest` |
| Pinned core MAF release | `go run ./cmd/dotnetsymbols -release 1.22.0` |
| Same version with the .NET release-tag prefix | `go run ./cmd/dotnetsymbols -release dotnet-1.22.0` |
| Latest release, only the abstractions package | `go run ./cmd/dotnetsymbols -package Microsoft.Agents.AI.Abstractions` |
| Selected core and provider packages | `go run ./cmd/dotnetsymbols -release 1.22.0 -package Microsoft.Agents.AI.Abstractions -package Microsoft.Agents.AI.OpenAI` |
| Latest release, a different target framework | `go run ./cmd/dotnetsymbols -framework net10.0` |
| Latest release from the public .NET package mirror | `go run ./cmd/dotnetsymbols -nuget-source https://pkgs.dev.azure.com/dnceng/public/_packaging/dotnet-public/nuget/v3/index.json` |

Without `-package`, the selected packages are `Microsoft.Agents.AI`, `Microsoft.Agents.AI.Abstractions`, and `Microsoft.Agents.AI.Workflows`. This is the core set, not every MAF integration. Repeating `-package` replaces that default set. Use `ID@version` when an explicitly selected package has a different version, such as a preview integration or a `Microsoft.Extensions.AI` dependency. Dependencies are not selected automatically.

Omitting `-release`, or passing `-release latest`, selects the highest stable version of `Microsoft.Agents.AI` advertised by the selected feed. Version components are compared numerically, prereleases are excluded, and the resolved version is used for every package without an explicit `ID@version` override. The version is resolved once per invocation, not independently for each package. If every package has an exact override, no latest-version lookup is needed. Missing packages at that release fail rather than falling back to older or prerelease versions.

Use an exact `-release` version for reproducible reruns; the output always records the actual package versions and hashes. The optional `dotnet-` prefix on a pinned version is removed without GitHub tag resolution. Version ranges and Git commits are not accepted. NuGet normalization removes leading numeric zeros, a zero fourth component, and `+build.metadata` for package addressing and identity checks; the manifest's original version is retained in the output. The reviewed mapping's commit baseline remains unchanged.

The default framework is `net8.0`. The command selects the exact `ref/<framework>` asset group when present, otherwise the exact `lib/<framework>` group. There is no compatibility or version fallback. Missing versions, missing framework groups, packages with no DLLs, conflicting package versions, and unsupported assemblies fail without emitting a partial inventory. Available asset groups are reported when the requested framework is absent.

The default source is `https://api.nuget.org/v3/index.json`; `-nuget-source` accepts another public HTTPS NuGet v3 service index. The selected source is recorded, with no silent mirror fallback. On hosts unable to establish TLS with NuGet.org, the public .NET mirror can be selected explicitly as shown above. Authentication and credential-provider integration are not supported.

Packages and selected assemblies are read in memory, never unpacked to the working tree. Package scripts, build targets, and assembly code are not executed. Requests have a two-minute timeout; package downloads and individual assembly assets are limited to 128 MiB. Progress and errors go to standard error, keeping standard output JSON-only. Package and assembly hashes identify downloaded bytes; they are not a NuGet signature-verification or trust policy.

Supplying `-assembly` selects local mode instead of the default release lookup and makes no network requests. Explicit `-release`, `-package`, `-framework`, or `-nuget-source` flags cannot be combined with `-assembly`. Namespace and protected-member filters work in both modes.

## Extract local assemblies

1. Check out the .NET revision being inventoried and use its required SDK. The mapping's current upstream baseline requires .NET SDK **10.0.303**; building it with an older SDK is not supported.
2. Build the relevant library projects in one configuration and target framework, rather than the full solution's tests and samples. Reference assemblies are preferred, but implementation assemblies work too. Keep all build output outside version control.
3. Pass explicit assembly files or narrowly scoped globs to the command. Include dependency assemblies such as `Microsoft.Extensions.AI.Abstractions` only when their public declarations belong in the inventory. Referencing a dependency does not cause it to be inventoried automatically.

Run from the Go repository root. Paths below are illustrative local build-output locations:

| Selection | Command |
| --- | --- |
| One assembly | `go run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll'` |
| Several selected assemblies | `go run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Workflows.dll'` |
| A dedicated reference-assembly directory | `go run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI*.dll'` |
| One namespace and its children | `go run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -namespace Microsoft.Agents.AI` |
| Public and externally inheritable API | `go run ./cmd/dotnetsymbols -assembly 'C:/temp/agent-api/Microsoft.Agents.AI.Abstractions.dll' -include-protected` |

Repeat `-namespace` to include multiple namespace trees. Without it, all namespaces in the selected assemblies are included. Do not assume every SDK extension uses a `Microsoft.Agents.AI` namespace: OpenAI and Azure client extensions are declared in their client namespaces.

Patterns use Go's `filepath.Glob` syntax, not recursive `**`. Unmatched patterns fail. Repeated identical assembly bytes are deduplicated; different inputs with the same assembly name fail, so accidentally mixing target frameworks or reference/implementation copies cannot silently merge API surfaces. Conflicting type definitions also fail. Output is buffered until every selected assembly has been processed successfully.

## Generated data

The inventory is a separate schema with `schema_version: 1` and `identity_format: ecma335-v1`:

- `selection`: namespace filters and protected-member policy.
- `packages`: present in release mode, keyed by package ID. Records exact version, source feed, download URL, package SHA-256, selected framework/asset group, and included assembly names. Repository URL and commit are retained when provided by the package manifest; a missing commit is not inferred from the version.
- `assemblies`: assembly version, informational version, target framework, SHA-256 of the input, reference-assembly marker, and unresolved type forwarders. Informational versions can carry a source revision even when the package manifest does not. For local builds, retain the source checkout revision separately; the extractor does not infer it from the current working directory.
- `types`: namespace-qualified CLR type names, declaring assembly, type kind, base type, generic parameter names/positions/constraints, and declared constructors, methods, properties, events, fields, and constants.

The inventory retains only declaration identities, matching metadata, review markers, and input provenance. Parameter names/defaults, constant values, raw attribute-name lists, nullable contexts, interface lists, and unused API flags/accessor details are omitted. Empty attribute objects and empty parameter lists are omitted as well. This is not a complete API contract; inspect pinned source for defaults, mutability, lifecycle, and behavioral reviews.

There are no per-symbol paths or line numbers. Inherited members are not repeated under derived types. Visibility is applied during selection, not repeated in the JSON: nested types require visibility through the entire enclosing chain, while properties/events use accessor visibility. By default only public API is selected; `-include-protected` adds protected and protected-internal API, not private-protected or internal declarations.

Ordinary accessor methods are not duplicated in the method list; operators and associated helper methods remain methods. Public compiler-generated record members remain present, with a compact `compiler_generated` marker. Enum constant names/types are included but their runtime `value__` storage field is excluded.

### Stable metadata signatures

The generated keys deliberately use metadata spelling, not the manual catalog's C# source spelling:

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

Type parameters use `!n`; method parameters use `!!n`. Return types remain in method/property keys so conversion operators and metadata-only overloads cannot collide. VARARG methods include a trailing `...` parameter marker. Array dimensions preserve lower-bound/size pairs, including omitted values. Custom modifiers and supported function-pointer calling conventions are retained. Duplicate canonical identities fail instead of overwriting a declaration. Names do not include assembly qualification in signatures; conflicting declarations, signatures, or same-name type references from different assembly scopes are rejected rather than merged.

Reference nullability does not change CLR overload identity. `nullable_flags` are retained on parameters/returns and property/event/field types where the reconciler uses them to distinguish nullable-reference spelling from nullable value types. They are not expanded into complete C# `?` spelling. Generic parameter positions and flags remain necessary for positional matching and value-type constraints. Attributes retain only these nullability flags, declared experimental diagnostic IDs, and compiler-generated markers.

### Boundaries

- This is **declaration extraction**, not Go counterpart discovery, behavioral verification, or automatic editing of mapping statuses.
- The reviewed mapping uses namespace groups and short source-like signatures. `go run ./cmd/symbolmap reconcile -summary` joins its references to this inventory and an index of the actual Go API, without changing assessments. Ambiguous identities stay unresolved; unreviewed declarations are not automatically gaps. See the [mapping and reconciliation guide](../../docs/dotnet-go-sdk-feature-comparison.md#reconcile-the-inventories).
- The current metadata reader requires compressed `#~` tables. It rejects some CLR signatures, including ref-return property signatures and modern unmanaged function pointers. For example, extracting `LinkedListNode<T>.ValueRef` from a runtime reference assembly fails explicitly. Unsupported selected signatures stop the inventory rather than being skipped.
- Multi-module assemblies are unsupported. Forwarded types are listed separately and are not loaded from their target assemblies.
- Obsolete annotations, arbitrary attribute arguments, accessor-only attributes, generic-parameter attributes, and constant values are not inventoried. Static-readonly values are not evaluated.
- Metadata does not reconstruct source aliases, tuple element spelling, `dynamic`, or runtime defaults computed by code. Use Roslyn if source-faithful C# display becomes a requirement.

Regenerating or trimming a snapshot changes its file hash. Existing mapping review batches retain the hash of the bytes originally inspected; an output-format change does not refresh their review dates or source baselines.

## Requirements

The extractor itself requires only Go. Release mode also requires HTTPS access to the selected NuGet feed; local mode requires already-built assemblies. Neither mode invokes a .NET SDK. Build upstream projects separately only when unpublished source must be inspected. No .NET source files, fixture projects, tests, or assembly binaries are included with this command. A generated [core inventory snapshot](../../docs/dotnet-sdk-symbol-inventory.json) is stored separately as JSON documentation.
