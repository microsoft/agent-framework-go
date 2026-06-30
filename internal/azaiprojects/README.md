# Microsoft Foundry Projects SDK

This package contains a temporary, internal Go SDK for the Microsoft Foundry Projects data-plane APIs used by Agent Framework.

We keep this SDK in-tree because the official Azure SDK for Go does not yet provide the AI Foundry Projects client surface needed by this repository. Once the official SDK supports these APIs, this package should be removed and callers should move back to the official Azure SDK module.

## Generation

The SDK is generated from TypeSpec using `go generate`:

```powershell
go generate ./internal/azaiprojects
```

The generation entrypoint is [gen.go](gen.go). It invokes `tsp-client` through pinned `npx`, so contributors need Node/npm but do not need a globally installed `tsp-client`.

The generated files use the `z` file prefix so hand-written package files can keep normal names such as [client.go](client.go). The hand-written client file provides the constructor and cloud configuration that the Go emitter intentionally omits when generating into an existing containing module.

## TypeSpec Source

The current TypeSpec source is pinned in [tsp-location.yaml](tsp-location.yaml). It points at a fork and commit that include the Go emitter configuration required for this internal SDK.

After [Azure/azure-rest-api-specs#44243](https://github.com/Azure/azure-rest-api-specs/pull/44243) is merged, the TypeSpec source can be moved from the fork to the Azure REST API Specs Foundry development branch that contains the merged Go SDK emitter configuration. That should make the pinned source less temporary while we wait for the official Azure SDK for Go package to expose the needed Foundry APIs.