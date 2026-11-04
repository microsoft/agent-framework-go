// Copyright (c) Microsoft. All rights reserved.

package symbolcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
)

type memberGroup struct {
	kind    string
	members *map[string]Mapping
}

func memberGroups(entry *Type) []memberGroup {
	return []memberGroup{
		{"constant", &entry.Constants},
		{"constructor", &entry.Constructors},
		{"event", &entry.Events},
		{"field", &entry.Fields},
		{"method", &entry.Methods},
		{"property", &entry.Properties},
	}
}

// DecodeInventory accepts optional API metadata, but strictly checks the
// name-only test schema. SHA256 pins the exact extraction bytes, not mappings.
func DecodeInventory(data []byte) (Inventory, error) {
	var inv Inventory
	if err := checkDocument(data); err != nil {
		return inv, err
	}
	input := struct {
		*Inventory
		Tests json.RawMessage `json:"tests"`
	}{Inventory: &inv}
	if err := json.Unmarshal(data, &input); err != nil {
		return Inventory{}, err
	}
	if len(input.Tests) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(input.Tests))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&inv.Tests); err != nil {
			return Inventory{}, fmt.Errorf("test inventory: %w", err)
		}
		if inv.Tests == nil {
			return Inventory{}, errors.New("test inventory tests must be a non-null object")
		}
	}
	if err := inv.Validate(); err != nil {
		return Inventory{}, err
	}
	inv.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	return inv, nil
}

func (inv Inventory) Validate() error {
	if inv.SchemaVersion != 1 {
		return fmt.Errorf("unsupported inventory schema_version %d", inv.SchemaVersion)
	}
	if inv.IdentityFormat != "ecma335-v1" {
		return fmt.Errorf("unsupported inventory identity_format %q", inv.IdentityFormat)
	}
	if len(inv.Assemblies) == 0 || len(inv.Types) == 0 {
		return errors.New("inventory assemblies and types must not be empty")
	}
	for _, name := range slices.Sorted(maps.Keys(inv.Assemblies)) {
		if !validHash(inv.Assemblies[name].SHA256) {
			return fmt.Errorf("inventory %s: API assembly SHA256 is required", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(inv.Types)) {
		typ := inv.Types[name]
		if _, exists := inv.Assemblies[typ.Assembly]; typ.Assembly == "" || !exists {
			return fmt.Errorf("type %q references unknown assembly %q", name, typ.Assembly)
		}
		if !ValidTypeName(name) {
			return fmt.Errorf("invalid canonical .NET type %q", name)
		}
	}
	return validateTestInventory(inv.Tests)
}

func validateTestInventory(inv *testinventory.Inventory) error {
	if inv == nil {
		return nil
	}
	if inv.IdentityFormat != testinventory.IdentityFormat {
		return fmt.Errorf("unsupported test inventory identity_format %q", inv.IdentityFormat)
	}
	if len(inv.Assemblies) == 0 {
		return errors.New("test inventory assemblies must not be empty")
	}
	for _, name := range slices.Sorted(maps.Keys(inv.Assemblies)) {
		assembly := inv.Assemblies[name]
		if !testAssemblyPattern.MatchString(name) || len(assembly.Types) == 0 {
			return fmt.Errorf("test inventory: invalid or empty assembly %q", name)
		}
		if err := validateTestAssembly(name, assembly.AssemblyMetadata); err != nil {
			return err
		}
		for _, owner := range slices.Sorted(maps.Keys(assembly.Types)) {
			methods := assembly.Types[owner]
			if !ValidTypeName(owner) || len(methods) == 0 {
				return fmt.Errorf("test inventory %s: invalid or empty metadata type %q", name, owner)
			}
			seen := make(map[string]bool, len(methods))
			for _, method := range methods {
				if !namePattern.MatchString(method) {
					return fmt.Errorf("test inventory %s %s: invalid test name %q", name, owner, method)
				}
				if seen[method] {
					return fmt.Errorf("test inventory %s %s: duplicate test name %q", name, owner, method)
				}
				seen[method] = true
			}
		}
	}
	return nil
}

func validateTestAssembly(name string, info testinventory.AssemblyMetadata) error {
	if info.Commit != "" && !validCommit(info.Commit) {
		return fmt.Errorf("test inventory %s: commit must be a full source revision", name)
	}
	if !validHash(info.SHA256) {
		return fmt.Errorf("test inventory %s: assembly SHA256 is required", name)
	}
	return nil
}

// Declarations projects the catalog's current labels, markers, and provenance.
// It is a reporting view, not a reconstruction of raw reflection metadata.
func (c Catalog) Declarations() (Inventory, error) {
	if c.Dotnet == nil {
		return Inventory{}, errors.New("catalog has no .NET declaration metadata")
	}
	meta := c.Dotnet
	if !validHash(meta.SHA256) {
		return Inventory{}, errors.New("dotnet: extraction SHA256 is required")
	}
	inv := Inventory{
		SchemaVersion: meta.SchemaVersion, IdentityFormat: meta.IdentityFormat,
		Selection: meta.Selection, Packages: meta.Packages, Assemblies: meta.Assemblies,
		Types: make(map[string]Declaration), SHA256: meta.SHA256,
		catalogLabels: make(map[string]string),
	}
	for _, namespace := range slices.Sorted(maps.Keys(c.Namespaces)) {
		for _, name := range slices.Sorted(maps.Keys(c.Namespaces[namespace])) {
			entry := c.Namespaces[namespace][name]
			full := namespace + "." + name
			if !c.includesType(namespace, entry) || entry.Unavailable {
				continue
			}
			identity := entry.Identity
			if identity == "" {
				t, ok := dnParse(name, 0)
				if !ok {
					return Inventory{}, fmt.Errorf("%s: invalid declaring type label", full)
				}
				identity = strings.ReplaceAll(t.name, ".", "+")
			}
			owner := namespace + "." + identity
			if !ValidTypeName(owner) {
				return Inventory{}, fmt.Errorf("%s: invalid declaring type identity", full)
			}
			if _, exists := inv.Types[owner]; exists {
				return Inventory{}, fmt.Errorf("duplicate canonical .NET type %q", owner)
			}
			kind := entry.Kind
			if kind == "" {
				kind = "class"
			}
			typ := Declaration{
				Assembly: entry.Assembly, Kind: kind,
				Attributes: Attributes{Experimental: entry.Experimental, CompilerGenerated: entry.Generated},
			}
			for _, group := range memberGroups(&entry) {
				for _, key := range slices.Sorted(maps.Keys(*group.members)) {
					m := (*group.members)[key]
					if m.Unavailable {
						continue
					}
					typ.addMember(group.kind, key, Attributes{Experimental: m.Experimental, CompilerGenerated: m.Generated})
				}
			}
			inv.Types[owner] = typ
			inv.catalogLabels[owner] = full
		}
	}
	if len(c.Tests) != 0 && meta.Tests == nil {
		return Inventory{}, errors.New("dotnet.tests: metadata is required for stored tests")
	}
	if meta.Tests != nil {
		if meta.Tests.IdentityFormat != testinventory.IdentityFormat || len(meta.Tests.Assemblies) == 0 {
			return Inventory{}, errors.New("dotnet.tests requires test-name-v1 and nonempty assemblies")
		}
		for _, assembly := range slices.Sorted(maps.Keys(c.Tests)) {
			if _, present := meta.Tests.Assemblies[assembly]; !present {
				return Inventory{}, fmt.Errorf("dotnet.tests: metadata is required for test assembly %q", assembly)
			}
		}
		inv.Tests = &testinventory.Inventory{
			IdentityFormat: meta.Tests.IdentityFormat,
			Assemblies:     make(map[string]testinventory.Assembly),
		}
		for _, assembly := range slices.Sorted(maps.Keys(meta.Tests.Assemblies)) {
			info := meta.Tests.Assemblies[assembly]
			if err := validateTestAssembly(assembly, info); err != nil {
				return Inventory{}, err
			}
			tests := testinventory.Assembly{AssemblyMetadata: info, Types: make(map[string][]string)}
			for owner, methods := range c.Tests[assembly] {
				for method := range methods {
					if !slices.Contains(meta.Tests.Unavailable[assembly][owner], method) {
						tests.Types[owner] = append(tests.Types[owner], method)
					}
				}
				slices.Sort(tests.Types[owner])
				if len(tests.Types[owner]) == 0 {
					delete(tests.Types, owner)
				}
			}
			inv.Tests.Assemblies[assembly] = tests
		}
		for assembly, types := range meta.Tests.Unavailable {
			if _, present := meta.Tests.Assemblies[assembly]; !present {
				return Inventory{}, fmt.Errorf("dotnet.tests.unavailable: unknown assembly %q", assembly)
			}
			for owner, methods := range types {
				seen := make(map[string]bool)
				if len(methods) == 0 {
					return Inventory{}, errors.New("dotnet.tests.unavailable: empty test list")
				}
				for _, method := range methods {
					if c.Tests[assembly][owner][method] == nil || seen[method] {
						return Inventory{}, fmt.Errorf("dotnet.tests.unavailable: absent, unpaired, or duplicate test %s %s.%s", assembly, owner, method)
					}
					seen[method] = true
				}
			}
		}
	}
	return inv, inv.Validate()
}

func (typ *Declaration) addMember(kind, key string, attrs Attributes) {
	switch kind {
	case "constructor", "method":
		members := &typ.Methods
		if kind == "constructor" {
			members = &typ.Constructors
		}
		if *members == nil {
			*members = make(map[string]Method)
		}
		(*members)[key] = Method{Attributes: attrs}
	case "property", "event", "field", "constant":
		switch kind {
		case "property":
			if typ.Properties == nil {
				typ.Properties = make(map[string]Property)
			}
			typ.Properties[key] = Property{Attributes: attrs}
		case "event":
			if typ.Events == nil {
				typ.Events = make(map[string]Event)
			}
			typ.Events[key] = Event{Attributes: attrs}
		default:
			members := &typ.Fields
			if kind == "constant" {
				members = &typ.Constants
			}
			if *members == nil {
				*members = make(map[string]Field)
			}
			(*members)[key] = Field{Attributes: attrs}
		}
	}
}

func (c Catalog) includesType(namespace string, typ Type) bool {
	if c.Dotnet == nil || !includesNamespace(namespace, c.Dotnet.Selection) {
		return false
	}
	_, selected := c.Dotnet.Assemblies[typ.Assembly]
	return selected
}

func includesNamespace(namespace string, selection Selection) bool {
	return len(selection.Namespaces) == 0 || slices.ContainsFunc(selection.Namespaces, func(prefix string) bool {
		return namespace == prefix || strings.HasPrefix(namespace, prefix+".")
	})
}
