// Copyright (c) Microsoft. All rights reserved.

package symbolcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
)

// Merge refreshes supplied assemblies without modifying either input. Existing
// assessments and pairs remain unchanged; extraction never assigns counterparts.
// Removed unreviewed declarations are dropped, but removed assessments are kept
// with an unavailable marker so reconciliation can report them. Refreshing a
// recorded package must still supply its previous assemblies; removing or
// replacing an assembly requires a separately reviewed scope change. Refreshing
// a package-owned assembly also requires all its recorded packages as inputs.
func Merge(c Catalog, inv Inventory) (Catalog, error) {
	if inv.catalogLabels != nil {
		return Catalog{}, fmt.Errorf("catalog reporting projection is not raw extraction")
	}
	if err := c.Validate(); err != nil {
		return Catalog{}, err
	}
	if err := inv.Validate(); err != nil {
		return Catalog{}, err
	}
	data, err := encodeJSON(c)
	if err != nil {
		return Catalog{}, err
	}
	var result Catalog
	if err := json.Unmarshal(data, &result); err != nil {
		return Catalog{}, err
	}
	if result.Dotnet != nil && (result.Dotnet.Selection.IncludeProtected != inv.Selection.IncludeProtected || !slices.Equal(result.Dotnet.Selection.Namespaces, inv.Selection.Namespaces)) {
		return Catalog{}, fmt.Errorf("extraction selection differs from the catalog; review scope changes before refreshing")
	}
	if inv.SHA256 == "" {
		data, err := encodeJSON(inv)
		if err != nil {
			return Catalog{}, err
		}
		inv.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	if result.Dotnet == nil {
		result.Dotnet = &Metadata{Assemblies: make(map[string]Assembly)}
	}
	meta := result.Dotnet
	meta.SchemaVersion, meta.IdentityFormat, meta.SHA256 = inv.SchemaVersion, inv.IdentityFormat, inv.SHA256
	meta.Selection = inv.Selection
	if meta.Assemblies == nil {
		meta.Assemblies = make(map[string]Assembly)
	}
	for _, id := range slices.Sorted(maps.Keys(meta.Packages)) {
		pkg, refreshed := inv.Packages[id]
		for _, assembly := range meta.Packages[id].Assemblies {
			_, supplied := inv.Assemblies[assembly]
			if supplied && !refreshed {
				return Catalog{}, fmt.Errorf("assembly %q belongs to package %q; refresh its owning package to preserve provenance", assembly, id)
			}
			if refreshed && (!supplied || !slices.Contains(pkg.Assemblies, assembly)) {
				return Catalog{}, fmt.Errorf("package %q no longer supplies assembly %q; review assembly scope changes before refreshing", id, assembly)
			}
		}
	}
	maps.Copy(meta.Assemblies, inv.Assemblies)
	if len(inv.Packages) != 0 {
		if meta.Packages == nil {
			meta.Packages = make(map[string]Package)
		}
		maps.Copy(meta.Packages, inv.Packages)
	}
	names := NewNames(inv)
	type location struct{ namespace, name string }
	owners := make(map[string]location)
	for _, namespace := range slices.Sorted(maps.Keys(result.Namespaces)) {
		for _, name := range slices.Sorted(maps.Keys(result.Namespaces[namespace])) {
			entry := result.Namespaces[namespace][name]
			full := namespace + "." + name
			canonical := ""
			if entry.Identity != "" {
				canonical = namespace + "." + entry.Identity
			} else if resolved := names.ResolveOwner(full); len(resolved) == 1 {
				canonical = resolved[0]
			} else if len(resolved) > 1 {
				return Catalog{}, fmt.Errorf("%s: ambiguous declaring type during extraction merge", full)
			}
			typ, present := inv.Types[canonical]
			if !present {
				if _, selected := inv.Assemblies[entry.Assembly]; selected && includesNamespace(namespace, inv.Selection) {
					entry.Unavailable = true
					entry.Kind, entry.Experimental, entry.Generated = "", "", false
					if entry.Mapping != nil && entry.Mapping.Unreviewed {
						entry.Mapping = nil
					}
					for _, group := range memberGroups(&entry) {
						clearMembers(*group.members)
					}
					if !hasMappings(entry) {
						delete(result.Namespaces[namespace], name)
						continue
					}
				}
				result.Namespaces[namespace][name] = entry
				continue
			}
			if entry.Assembly != "" && entry.Assembly != typ.Assembly {
				return Catalog{}, fmt.Errorf("%s: declaring assembly changed from %q to %q; review the mapping before refreshing", full, entry.Assembly, typ.Assembly)
			}
			if _, duplicate := owners[canonical]; duplicate {
				return Catalog{}, fmt.Errorf("multiple catalog types resolve to %q", canonical)
			}
			owners[canonical] = location{namespace, name}
			setTypeMetadata(&entry, names, namespace, name, canonical, typ)
			fresh := extractedMembers(typ)
			for _, group := range memberGroups(&entry) {
				claimed := make(map[string]bool)
				updated := make(map[string]Mapping, len(*group.members))
				for _, key := range slices.Sorted(maps.Keys(*group.members)) {
					m := (*group.members)[key]
					canonicalKey := ""
					if m.Identity != "" {
						canonicalKey = m.Identity
					} else if resolved := names.ResolveMember(canonical, group.kind, full, key); len(resolved) == 1 {
						canonicalKey = resolved[0]
					} else if len(resolved) > 1 {
						return Catalog{}, fmt.Errorf("%s.%s: ambiguous member during extraction merge", full, key)
					}
					attrs, present := fresh[group.kind][canonicalKey]
					if !present {
						if !m.Unreviewed {
							m.Unavailable = true
							m.Experimental, m.Generated = "", false
							if _, occupied := updated[key]; occupied {
								return Catalog{}, fmt.Errorf("%s.%s: updated member label collides with an unavailable assessment", full, key)
							}
							updated[key] = m
						}
						continue
					}
					if claimed[canonicalKey] {
						return Catalog{}, fmt.Errorf("%s: multiple mappings resolve to %s %q", full, group.kind, canonicalKey)
					}
					claimed[canonicalKey] = true
					label := key
					if group.kind == "method" {
						var err error
						label, err = names.methodLabel(canonical, full, key, canonicalKey)
						if err != nil {
							return Catalog{}, err
						}
					}
					if _, occupied := updated[label]; occupied {
						return Catalog{}, fmt.Errorf("%s.%s: updated member label collision", full, label)
					}
					setMemberMetadata(&m, names, canonical, group.kind, full, label, canonicalKey, attrs)
					updated[label] = m
				}
				if len(updated) == 0 {
					updated = nil
				}
				*group.members = updated
			}
			result.Namespaces[namespace][name] = entry
		}
		if len(result.Namespaces[namespace]) == 0 {
			delete(result.Namespaces, namespace)
		}
	}
	for _, owner := range slices.Sorted(maps.Keys(inv.Types)) {
		typ := inv.Types[owner]
		loc, present := owners[owner]
		if !present {
			loc.namespace, loc.name = names.TypeLabel(owner)
			if result.Namespaces[loc.namespace] == nil {
				result.Namespaces[loc.namespace] = make(map[string]Type)
			}
			if _, occupied := result.Namespaces[loc.namespace][loc.name]; occupied {
				return Catalog{}, fmt.Errorf("%s: generated type label collides with an unresolved assessment", owner)
			}
			result.Namespaces[loc.namespace][loc.name] = Type{Area: defaultArea(loc.namespace)}
		}
		entry := result.Namespaces[loc.namespace][loc.name]
		setTypeMetadata(&entry, names, loc.namespace, loc.name, owner, typ)
		if entry.Mapping == nil {
			m := unreviewedMapping("type")
			entry.Mapping = &m
		}
		fresh := extractedMembers(typ)
		for _, group := range memberGroups(&entry) {
			claimed := make(map[string]bool)
			full := loc.namespace + "." + loc.name
			for label, m := range *group.members {
				if m.Unavailable {
					continue
				}
				if m.Identity != "" {
					claimed[m.Identity] = true
				} else if keys := names.ResolveMember(owner, group.kind, full, label); len(keys) == 1 {
					claimed[keys[0]] = true
				}
			}
			for _, key := range slices.Sorted(maps.Keys(fresh[group.kind])) {
				if claimed[key] {
					continue
				}
				label := names.MemberLabel(owner, group.kind, key)
				if _, occupied := (*group.members)[label]; occupied {
					return Catalog{}, fmt.Errorf("%s.%s: generated member label collides with an unresolved assessment", owner, label)
				}
				if *group.members == nil {
					*group.members = make(map[string]Mapping)
				}
				m := unreviewedMapping(group.kind)
				setMemberMetadata(&m, names, owner, group.kind, full, label, key, fresh[group.kind][key])
				(*group.members)[label] = m
			}
		}
		result.Namespaces[loc.namespace][loc.name] = entry
	}
	if inv.Tests != nil {
		mergeTests(&result, inv.Tests)
	}
	if err := result.Validate(); err != nil {
		return Catalog{}, err
	}
	return result, nil
}

func setTypeMetadata(entry *Type, names *Names, namespace, label, owner string, typ Declaration) {
	entry.Identity = ""
	if matches := names.ResolveOwner(namespace + "." + label); len(matches) != 1 || matches[0] != owner {
		entry.Identity = strings.TrimPrefix(owner, namespace+".")
	}
	entry.Assembly, entry.Kind = typ.Assembly, typ.Kind
	if entry.Kind == "class" {
		entry.Kind = ""
	}
	entry.Experimental, entry.Generated = typ.Attributes.Experimental, typ.Attributes.CompilerGenerated
	entry.Unavailable = false
}

func setMemberMetadata(m *Mapping, names *Names, owner, kind, sourceType, label, key string, attrs Attributes) {
	m.Identity = ""
	if matches := names.ResolveMember(owner, kind, sourceType, label); len(matches) != 1 || matches[0] != key {
		m.Identity = key
	}
	m.Experimental, m.Generated = attrs.Experimental, attrs.CompilerGenerated
	m.Unavailable = false
}

func unreviewedMapping(kind string) Mapping {
	m := Mapping{GoSymbols: []string{}, Status: "unmapped", Unreviewed: true}
	if kind != "property" {
		m.Go = new("")
	}
	return m
}

func clearMembers(members map[string]Mapping) {
	for key, m := range members {
		if m.Unreviewed {
			delete(members, key)
		} else {
			m.Unavailable = true
			m.Experimental, m.Generated = "", false
			members[key] = m
		}
	}
}

func hasMappings(entry Type) bool {
	if entry.Mapping != nil {
		return true
	}
	for _, group := range memberGroups(&entry) {
		if len(*group.members) != 0 {
			return true
		}
	}
	return false
}

func defaultArea(namespace string) string {
	switch {
	case namespace == "Microsoft.Agents.AI.Workflows" || strings.HasPrefix(namespace, "Microsoft.Agents.AI.Workflows."):
		return "workflows"
	case namespace == "Microsoft.Agents.AI" || strings.HasPrefix(namespace, "Microsoft.Agents.AI.Compaction"):
		return "agents"
	default:
		return "unclassified"
	}
}

func extractedMembers(typ Declaration) map[string]map[string]Attributes {
	result := make(map[string]map[string]Attributes)
	for _, kind := range []string{"constant", "constructor", "event", "field", "method", "property"} {
		result[kind] = make(map[string]Attributes)
	}
	for kind, methods := range map[string]map[string]Method{"constructor": typ.Constructors, "method": typ.Methods} {
		for key, m := range methods {
			result[kind][key] = m.Attributes
		}
	}
	for key, p := range typ.Properties {
		result["property"][key] = p.Attributes
	}
	for key, e := range typ.Events {
		result["event"][key] = e.Attributes
	}
	for kind, fields := range map[string]map[string]Field{"field": typ.Fields, "constant": typ.Constants} {
		for key, f := range fields {
			result[kind][key] = f.Attributes
		}
	}
	return result
}

func mergeTests(c *Catalog, inv *testinventory.Inventory) {
	if c.Tests == nil {
		c.Tests = make(TestMappings)
	}
	if c.Dotnet.Tests == nil {
		c.Dotnet.Tests = &TestMetadata{
			Assemblies: make(map[string]testinventory.AssemblyMetadata),
		}
	}
	meta := c.Dotnet.Tests
	meta.IdentityFormat = inv.IdentityFormat
	if meta.Unavailable == nil {
		meta.Unavailable = make(map[string]map[string][]string)
	}
	for _, name := range slices.Sorted(maps.Keys(inv.Assemblies)) {
		assembly := inv.Assemblies[name]
		meta.Assemblies[name] = assembly.AssemblyMetadata
		previous := c.Tests[name]
		current := make(map[string]map[string]*string)
		delete(meta.Unavailable, name)
		for owner, methods := range assembly.Types {
			current[owner] = make(map[string]*string, len(methods))
			for _, method := range methods {
				current[owner][method] = previous[owner][method]
			}
		}
		for _, owner := range slices.Sorted(maps.Keys(previous)) {
			for _, method := range slices.Sorted(maps.Keys(previous[owner])) {
				target := previous[owner][method]
				if _, exists := current[owner][method]; exists || target == nil {
					continue
				}
				if current[owner] == nil {
					current[owner] = make(map[string]*string)
				}
				current[owner][method] = target
				if meta.Unavailable[name] == nil {
					meta.Unavailable[name] = make(map[string][]string)
				}
				meta.Unavailable[name][owner] = append(meta.Unavailable[name][owner], method)
			}
		}
		c.Tests[name] = current
	}
}

// Encode keeps test pairs as single-line strings/nulls and emits stable JSON.
func Encode(c Catalog) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return encodeJSON(c)
}

func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
