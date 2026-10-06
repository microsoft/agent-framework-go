// Copyright (c) Microsoft. All rights reserved.

// Package testinventory defines the test declaration metadata shared by the
// inventory extractor and mapping reporter. It contains no mapping decisions.
package testinventory

// IdentityFormat identifies tests by assembly, CLR declaring type, and name.
const IdentityFormat = "test-name-v1"

// Inventory records the selected test assemblies, not all runtime test cases.
type Inventory struct {
	IdentityFormat string              `json:"identity_format"`
	Assemblies     map[string]Assembly `json:"assemblies"`
}

// AssemblyMetadata identifies the exact input bytes and their build metadata.
// Commit is omitted when the assembly does not record a full source revision.
type AssemblyMetadata struct {
	Commit          string `json:"commit,omitempty"`
	TargetFramework string `json:"target_framework,omitempty"`
	SHA256          string `json:"sha256"`
}

// Assembly groups sorted test method names by their CLR declaring type.
type Assembly struct {
	AssemblyMetadata
	Types map[string][]string `json:"types"`
}
