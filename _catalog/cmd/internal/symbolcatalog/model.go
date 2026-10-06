// Copyright (c) Microsoft. All rights reserved.

// Package symbolcatalog stores extracted .NET declarations and their Go
// mappings together, without inferring counterparts from declaration names.
package symbolcatalog

import "github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"

// Catalog is the single maintained declaration and mapping document.
type Catalog struct {
	SchemaVersion *int                        `json:"schema_version"`
	Baseline      Baseline                    `json:"baseline"`
	Dotnet        *Metadata                   `json:"dotnet,omitempty"`
	GoOnly        map[string]GoOnlyAssessment `json:"go_only,omitempty"`
	Tests         TestMappings                `json:"tests,omitempty"`
	Namespaces    map[string]map[string]Type  `json:"namespaces"`
}

// Baseline pins the original assessments, not the latest extraction.
type Baseline struct {
	CheckedAt         string `json:"checked_at"`
	DotnetRepository  string `json:"dotnet_repository"`
	DotnetCommit      string `json:"dotnet_commit"`
	GoRepository      string `json:"go_repository"`
	GoCommit          string `json:"go_commit"`
	GoModule          string `json:"go_module"`
	InventoryComplete *bool  `json:"inventory_complete"`
	Scope             string `json:"scope"`
}

// Metadata describes extraction inputs; declarations live only in Namespaces
// and Tests. SHA256 identifies the extractor output used for this refresh.
type Metadata struct {
	SchemaVersion  int                 `json:"schema_version"`
	IdentityFormat string              `json:"identity_format"`
	SHA256         string              `json:"sha256"`
	Selection      Selection           `json:"selection"`
	Packages       map[string]Package  `json:"packages,omitempty"`
	Assemblies     map[string]Assembly `json:"assemblies"`
	Tests          *TestMetadata       `json:"tests,omitempty"`
}

// TestMetadata retains build provenance without duplicating the test list.
// Unavailable records only retained pairs absent from a refreshed assembly.
type TestMetadata struct {
	IdentityFormat string                                    `json:"identity_format"`
	Assemblies     map[string]testinventory.AssemblyMetadata `json:"assemblies"`
	Unavailable    map[string]map[string][]string            `json:"unavailable,omitempty"`
}

// Type groups declaration labels and their mappings. Class is the default kind;
// Identity overrides the declaring CLR name only when the label is insufficient.
type Type struct {
	Area         string             `json:"area"`
	Assembly     string             `json:"assembly,omitempty"`
	Identity     string             `json:"identity,omitempty"`
	Kind         string             `json:"kind,omitempty"`
	Experimental string             `json:"experimental,omitempty"`
	Generated    bool               `json:"compiler_generated,omitempty"`
	Unavailable  bool               `json:"unavailable,omitempty"`
	Mapping      *Mapping           `json:"mapping,omitempty"`
	Properties   map[string]Mapping `json:"properties,omitempty"`
	Methods      map[string]Mapping `json:"methods,omitempty"`
	Constructors map[string]Mapping `json:"constructors,omitempty"`
	Fields       map[string]Mapping `json:"fields,omitempty"`
	Constants    map[string]Mapping `json:"constants,omitempty"`
	Events       map[string]Mapping `json:"events,omitempty"`
}

// Mapping preserves an assessment independently of extracted metadata.
// Unreviewed entries have no counterparts or assessment notes.
// Notes are optional for mapped entries and required for other assessments.
type Mapping struct {
	Go           *string  `json:"go,omitempty"`
	GoSymbols    []string `json:"go_symbols"`
	Status       string   `json:"status"`
	Unreviewed   bool     `json:"unreviewed,omitempty"`
	Note         string   `json:"note,omitempty"`
	Identity     string   `json:"identity,omitempty"`
	Experimental string   `json:"experimental,omitempty"`
	Generated    bool     `json:"compiler_generated,omitempty"`
	Unavailable  bool     `json:"unavailable,omitempty"`
}

type GoOnlyAssessment struct {
	Note string `json:"note"`
}

// TestMappings use null for unpaired declarations and a unique Go test string
// for paired declarations. Tests have no per-test notes or review objects.
type TestMappings map[string]map[string]map[string]*string

// Inventory is the transient extraction interchange format, not a maintained
// second catalog. The extractor can emit it without modifying a catalog.
type Inventory struct {
	SchemaVersion  int                      `json:"schema_version"`
	IdentityFormat string                   `json:"identity_format"`
	Selection      Selection                `json:"selection"`
	Packages       map[string]Package       `json:"packages,omitempty"`
	Assemblies     map[string]Assembly      `json:"assemblies"`
	Types          map[string]Declaration   `json:"types"`
	Tests          *testinventory.Inventory `json:"tests,omitempty"`
	SHA256         string                   `json:"-"`

	// Catalog projections contain authoritative labels, not reflection data.
	// Raw extraction continues to use canonical signatures and full matching.
	catalogLabels map[string]string
}

type Package struct {
	Version    string   `json:"version"`
	Source     string   `json:"source"`
	Download   string   `json:"download"`
	SHA256     string   `json:"sha256"`
	Framework  string   `json:"framework"`
	AssetGroup string   `json:"asset_group"`
	Repository string   `json:"repository,omitempty"`
	Commit     string   `json:"commit,omitempty"`
	Assemblies []string `json:"assemblies"`
}

type Selection struct {
	Namespaces       []string `json:"namespaces"`
	IncludeProtected bool     `json:"include_protected"`
}

type Assembly struct {
	Version              string            `json:"version"`
	InformationalVersion string            `json:"informational_version,omitempty"`
	TargetFramework      string            `json:"target_framework,omitempty"`
	SHA256               string            `json:"sha256"`
	ReferenceAssembly    bool              `json:"reference_assembly"`
	ForwardedTypes       map[string]string `json:"forwarded_types,omitempty"`
}

type Declaration struct {
	Assembly          string              `json:"assembly"`
	Kind              string              `json:"kind"`
	BaseType          string              `json:"base_type,omitempty"`
	GenericParameters []Generic           `json:"generic_parameters,omitempty"`
	Attributes        Attributes          `json:"attributes,omitzero"`
	Constructors      map[string]Method   `json:"constructors,omitempty"`
	Methods           map[string]Method   `json:"methods,omitempty"`
	Properties        map[string]Property `json:"properties,omitempty"`
	Events            map[string]Event    `json:"events,omitempty"`
	Fields            map[string]Field    `json:"fields,omitempty"`
	Constants         map[string]Field    `json:"constants,omitempty"`
}

type Generic struct {
	Name        string   `json:"name"`
	Number      uint16   `json:"number"`
	Flags       uint16   `json:"flags,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}

type Method struct {
	VarArgs           bool        `json:"varargs,omitempty"`
	ReturnType        string      `json:"return_type"`
	ReturnAttributes  Attributes  `json:"return_attributes,omitzero"`
	Parameters        []Parameter `json:"parameters,omitempty"`
	GenericParameters []Generic   `json:"generic_parameters,omitempty"`
	Attributes        Attributes  `json:"attributes,omitzero"`
}

type Parameter struct {
	Type       string     `json:"type"`
	Modifier   string     `json:"modifier,omitempty"`
	Attributes Attributes `json:"attributes,omitzero"`
}

type Property struct {
	Type       string      `json:"type"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Attributes Attributes  `json:"attributes,omitzero"`
}

type Event struct {
	Type       string     `json:"type"`
	Attributes Attributes `json:"attributes,omitzero"`
}

type Field struct {
	Type       string     `json:"type"`
	Attributes Attributes `json:"attributes,omitzero"`
}

type Attributes struct {
	Experimental      string `json:"experimental,omitempty"`
	CompilerGenerated bool   `json:"compiler_generated,omitempty"`
	NullableFlags     []int  `json:"nullable_flags,omitempty"`
}
