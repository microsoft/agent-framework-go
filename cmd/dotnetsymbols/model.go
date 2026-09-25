// Copyright (c) Microsoft. All rights reserved.

package main

// The generated inventory deliberately contains no Go mapping decisions.
// It retains declaration identities and metadata needed for reconciliation,
// not a complete description of API behavior or C# source text.
type inventory struct {
	SchemaVersion  int                     `json:"schema_version"`
	IdentityFormat string                  `json:"identity_format"`
	Selection      selection               `json:"selection"`
	Packages       map[string]packageInfo  `json:"packages,omitempty"`
	Assemblies     map[string]assemblyInfo `json:"assemblies"`
	Types          map[string]typeInfo     `json:"types"`
}

type packageInfo struct {
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

type selection struct {
	Namespaces       []string `json:"namespaces"`
	IncludeProtected bool     `json:"include_protected"`
}

type assemblyInfo struct {
	Version              string            `json:"version"`
	InformationalVersion string            `json:"informational_version,omitempty"`
	TargetFramework      string            `json:"target_framework,omitempty"`
	SHA256               string            `json:"sha256"`
	ReferenceAssembly    bool              `json:"reference_assembly"`
	ForwardedTypes       map[string]string `json:"forwarded_types,omitempty"`
}

type typeInfo struct {
	Assembly          string                  `json:"assembly"`
	Kind              string                  `json:"kind"`
	BaseType          string                  `json:"base_type,omitempty"`
	GenericParameters []genericParameter      `json:"generic_parameters,omitempty"`
	Attributes        attributes              `json:"attributes,omitzero"`
	Constructors      map[string]methodInfo   `json:"constructors,omitempty"`
	Methods           map[string]methodInfo   `json:"methods,omitempty"`
	Properties        map[string]propertyInfo `json:"properties,omitempty"`
	Events            map[string]eventInfo    `json:"events,omitempty"`
	Fields            map[string]fieldInfo    `json:"fields,omitempty"`
	Constants         map[string]fieldInfo    `json:"constants,omitempty"`
}

type genericParameter struct {
	Name        string   `json:"name"`
	Number      uint16   `json:"number"`
	Flags       uint16   `json:"flags,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}

type methodInfo struct {
	VarArgs           bool               `json:"varargs,omitempty"`
	ReturnType        string             `json:"return_type"`
	ReturnAttributes  attributes         `json:"return_attributes,omitzero"`
	Parameters        []parameterInfo    `json:"parameters,omitempty"`
	GenericParameters []genericParameter `json:"generic_parameters,omitempty"`
	Attributes        attributes         `json:"attributes,omitzero"`
}

type parameterInfo struct {
	Type       string     `json:"type"`
	Modifier   string     `json:"modifier,omitempty"`
	Attributes attributes `json:"attributes,omitzero"`
}

type propertyInfo struct {
	Type       string          `json:"type"`
	Parameters []parameterInfo `json:"parameters,omitempty"`
	Attributes attributes      `json:"attributes,omitzero"`
}

type eventInfo struct {
	Type       string     `json:"type"`
	Attributes attributes `json:"attributes,omitzero"`
}

type fieldInfo struct {
	Type       string     `json:"type"`
	Attributes attributes `json:"attributes,omitzero"`
}

type attributes struct {
	Experimental      string `json:"experimental,omitempty"`
	CompilerGenerated bool   `json:"compiler_generated,omitempty"`
	NullableFlags     []int  `json:"nullable_flags,omitempty"`
}
