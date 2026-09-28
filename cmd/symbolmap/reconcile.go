// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

// These models read the metadata needed for identity resolution and provenance.
// Optional metadata added by the extractor does not change this contract.
type declarationInventory struct {
	SchemaVersion  int                            `json:"schema_version"`
	IdentityFormat string                         `json:"identity_format"`
	Selection      declarationSelection           `json:"selection"`
	Packages       map[string]declarationPackage  `json:"packages,omitempty"`
	Assemblies     map[string]declarationAssembly `json:"assemblies"`
	Types          map[string]declarationType     `json:"types"`
	SHA256         string                         `json:"-"`
}

type declarationSelection struct {
	Namespaces       []string `json:"namespaces"`
	IncludeProtected bool     `json:"include_protected"`
}

type declarationPackage struct {
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

type declarationAssembly struct {
	InformationalVersion string            `json:"informational_version,omitempty"`
	Version              string            `json:"version"`
	TargetFramework      string            `json:"target_framework,omitempty"`
	SHA256               string            `json:"sha256"`
	ReferenceAssembly    bool              `json:"reference_assembly"`
	ForwardedTypes       map[string]string `json:"forwarded_types,omitempty"`
}

type declarationType struct {
	Assembly          string                         `json:"assembly"`
	Kind              string                         `json:"kind"`
	BaseType          string                         `json:"base_type,omitempty"`
	GenericParameters []declarationGeneric           `json:"generic_parameters,omitempty"`
	Attributes        declarationAttributes          `json:"attributes,omitzero"`
	Constructors      map[string]declarationMethod   `json:"constructors,omitempty"`
	Methods           map[string]declarationMethod   `json:"methods,omitempty"`
	Properties        map[string]declarationProperty `json:"properties,omitempty"`
	Events            map[string]declarationEvent    `json:"events,omitempty"`
	Fields            map[string]declarationField    `json:"fields,omitempty"`
	Constants         map[string]declarationField    `json:"constants,omitempty"`
}

type declarationGeneric struct {
	Name        string   `json:"name"`
	Number      uint16   `json:"number"`
	Flags       uint16   `json:"flags,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}

type declarationMethod struct {
	VarArgs           bool                   `json:"varargs,omitempty"`
	ReturnType        string                 `json:"return_type"`
	ReturnAttributes  declarationAttributes  `json:"return_attributes,omitzero"`
	Parameters        []declarationParameter `json:"parameters,omitempty"`
	GenericParameters []declarationGeneric   `json:"generic_parameters,omitempty"`
	Attributes        declarationAttributes  `json:"attributes,omitzero"`
}

type declarationParameter struct {
	Type       string                `json:"type"`
	Modifier   string                `json:"modifier,omitempty"`
	Attributes declarationAttributes `json:"attributes,omitzero"`
}

type declarationProperty struct {
	Type       string                 `json:"type"`
	Parameters []declarationParameter `json:"parameters,omitempty"`
	Attributes declarationAttributes  `json:"attributes,omitzero"`
}

type declarationEvent struct {
	Type       string                `json:"type"`
	Attributes declarationAttributes `json:"attributes,omitzero"`
}

type declarationField struct {
	Type       string                `json:"type"`
	Attributes declarationAttributes `json:"attributes,omitzero"`
}

type declarationAttributes struct {
	Experimental      string `json:"experimental,omitempty"`
	CompilerGenerated bool   `json:"compiler_generated,omitempty"`
	NullableFlags     []int  `json:"nullable_flags,omitempty"`
}

// Canonical identities are internal join keys, not presentation strings.
type declarationRef struct {
	owner string
	kind  string
	key   string
}

var reconciliationStates = []string{
	"linked", "unreviewed", "needs-reconciliation", "invalid-go-target",
	"outside-inventory-scope", "go-outside-scope",
}

type reconciliationRow struct {
	Namespace           string   `json:"namespace"`
	Type                string   `json:"type"`
	Member              string   `json:"member"`
	Dotnet              string   `json:"dotnet"`
	Assembly            string   `json:"assembly"`
	Area                string   `json:"area"`
	Kind                string   `json:"kind"`
	State               string   `json:"state"`
	Go                  *string  `json:"go,omitempty"`
	GoSymbols           []string `json:"go_symbols"`
	Status              string   `json:"status,omitempty"`
	Note                string   `json:"note,omitempty"`
	Review              string   `json:"review,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	Candidates          []string `json:"candidates,omitempty"`
	SuggestedGo         []string `json:"suggested_go,omitempty"`
	InvalidGoTargets    []string `json:"invalid_go_targets,omitempty"`
	UnindexedGoTargets  []string `json:"unindexed_go_targets,omitempty"`
	Experimental        string   `json:"experimental,omitempty"`
	CompilerGenerated   bool     `json:"compiler_generated,omitempty"`
	LanguageMachinery   bool     `json:"language_machinery,omitempty"`
	ReviewSourceChanged *bool    `json:"review_source_changed,omitempty"`
	ReviewGoChanged     *bool    `json:"review_go_changed,omitempty"`
}

type reconciliationInventory struct {
	SchemaVersion  int                            `json:"schema_version"`
	IdentityFormat string                         `json:"identity_format"`
	SHA256         string                         `json:"sha256"`
	Selection      declarationSelection           `json:"selection"`
	Packages       map[string]declarationPackage  `json:"packages,omitempty"`
	Assemblies     map[string]declarationAssembly `json:"assemblies"`
}

type reconciliationGoInventory struct {
	Module          string   `json:"module"`
	Commit          string   `json:"commit,omitempty"`
	Dirty           *bool    `json:"dirty,omitempty"`
	GOOS            string   `json:"goos"`
	GOARCH          string   `json:"goarch"`
	GoVersion       string   `json:"go_version"`
	CGOEnabled      string   `json:"cgo_enabled"`
	BuildTags       string   `json:"build_tags,omitempty"`
	Packages        []string `json:"packages"`
	ExportedSymbols int      `json:"exported_symbols"`
}

type reconciliationReport struct {
	Baseline              baseline                  `json:"baseline"`
	Reviews               map[string]reviewBaseline `json:"reviews,omitempty"`
	Inventory             reconciliationInventory   `json:"inventory"`
	Go                    reconciliationGoInventory `json:"go"`
	InventoryDeclarations int                       `json:"inventory_declarations"`
	AssessedDeclarations  int                       `json:"assessed_declarations"`
	Counts                map[string]int            `json:"counts"`
	ByArea                map[string]map[string]int `json:"by_area"`
	Rows                  []reconciliationRow       `json:"rows"`
	Page                  *pageInfo                 `json:"page,omitempty"`
}

func loadDeclarationInventory(file string) (declarationInventory, error) {
	var inv declarationInventory
	data, err := os.ReadFile(file)
	if err != nil {
		return inv, err
	}
	if root := bytes.TrimSpace(data); len(root) == 0 || root[0] != '{' {
		return inv, fmt.Errorf("%s: expected a single JSON object", file)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := checkJSONKeys(decoder); err != nil {
		return inv, fmt.Errorf("%s: %w", file, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return inv, fmt.Errorf("%s: expected a single JSON object", file)
	}
	// Unknown optional fields are deliberately accepted, including fields in
	// attribute records. The key check still covers the entire input document.
	if err := json.Unmarshal(data, &inv); err != nil {
		return declarationInventory{}, fmt.Errorf("%s: %w", file, err)
	}
	if inv.SchemaVersion != 1 {
		return declarationInventory{}, fmt.Errorf("%s: unsupported inventory schema_version %d", file, inv.SchemaVersion)
	}
	if inv.IdentityFormat != "ecma335-v1" {
		return declarationInventory{}, fmt.Errorf("%s: unsupported inventory identity_format %q", file, inv.IdentityFormat)
	}
	if len(inv.Assemblies) == 0 || len(inv.Types) == 0 {
		return declarationInventory{}, fmt.Errorf("%s: inventory assemblies and types must not be empty", file)
	}
	for _, name := range slices.Sorted(maps.Keys(inv.Types)) {
		typ := inv.Types[name]
		if _, exists := inv.Assemblies[typ.Assembly]; typ.Assembly == "" || !exists {
			return declarationInventory{}, fmt.Errorf("%s: type %q references unknown assembly %q", file, name, typ.Assembly)
		}
	}
	inv.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	return inv, nil
}

func reconcile(report mappingsReport, inv declarationInventory, goAPI goInventory, allGoPackages bool) reconciliationReport {
	names := newDotnetNames(inv)
	result := reconciliationReport{
		Baseline: report.Baseline,
		Reviews:  report.Reviews,
		Inventory: reconciliationInventory{
			SchemaVersion: inv.SchemaVersion, IdentityFormat: inv.IdentityFormat, SHA256: inv.SHA256,
			Selection: inv.Selection, Packages: inv.Packages, Assemblies: inv.Assemblies,
		},
		Go: reconciliationGoInventory{
			Module: goAPI.Module, Commit: goAPI.Commit, Dirty: goAPI.Dirty,
			GOOS: goAPI.GOOS, GOARCH: goAPI.GOARCH, GoVersion: goAPI.GoVersion,
			CGOEnabled: goAPI.CGOEnabled, BuildTags: goAPI.BuildTags,
			Packages: goAPI.Packages, ExportedSymbols: len(goAPI.Symbols),
		},
		Rows: make([]reconciliationRow, 0),
	}
	goPackages := make(map[string]bool, len(goAPI.Packages))
	for _, pkg := range goAPI.Packages {
		goPackages[pkg] = true
	}
	sourceCommits := make(map[string]string, len(inv.Assemblies))
	for assembly := range inv.Assemblies {
		sourceCommits[assembly] = declarationSourceCommit(inv, assembly)
	}

	sourceRows := make([]reconciliationRow, len(report.Mappings))
	consumed := make([]bool, len(report.Mappings))
	claims := make(map[declarationRef][]int)
	typeAreas := make(map[string]map[string]bool)
	typeTargets := make(map[string][]string)
	for i, source := range report.Mappings {
		row := reconciliationRow{
			Namespace: source.Namespace, Type: source.Type, Member: source.Member,
			Assembly: source.Assembly, Area: source.Area, Kind: source.Kind,
			State: "needs-reconciliation", Go: source.Go, GoSymbols: append([]string{}, source.GoSymbols...),
			Status: source.Status, Note: source.Note, Review: source.Review,
		}
		if row.Kind == "type" {
			row.Member = ""
		}
		owners := names.resolveOwner(source.typeName)
		var ref declarationRef
		matched := false
		switch len(owners) {
		case 0:
			row.Reason = "declaring type was not resolved in the selected inventory; scope or baseline differences may be responsible"
			if source.Assembly != "" {
				if _, selected := inv.Assemblies[source.Assembly]; !selected {
					row.State = "outside-inventory-scope"
					row.Reason = "the recorded declaring assembly is not among the selected inventory assemblies"
				}
			}
			if row.State != "outside-inventory-scope" && row.Namespace != "" && !declarationIncludesNamespace(row.Namespace, inv.Selection.Namespaces) {
				row.State = "outside-inventory-scope"
				row.Reason = "the declaring namespace is excluded by the inventory namespace selection"
			}
			// A short-name proposal is not an alternative way to claim a type
			// whose fully qualified source identity failed to resolve.
			for _, owner := range names.resolveType(source.Type) {
				row.Candidates = append(row.Candidates, qualifiedDeclarationLabel(names, declarationRef{owner: owner, kind: "type"}))
			}
		case 1:
			owner := owners[0]
			typ := inv.Types[owner]
			row.Namespace, row.Type = names.typeLabel(owner)
			ref = declarationRef{owner: owner, kind: row.Kind}
			if source.Assembly != "" && source.Assembly != typ.Assembly {
				row.Reason = fmt.Sprintf("mapping records assembly %q, but the inventory declares this type in %q", source.Assembly, typ.Assembly)
				row.Candidates = []string{qualifiedDeclarationLabel(names, declarationRef{owner: owner, kind: "type"})}
				break
			}
			row.Assembly = typ.Assembly
			row.Experimental = typ.Attributes.Experimental
			if source.Area != "" {
				if typeAreas[owner] == nil {
					typeAreas[owner] = make(map[string]bool)
				}
				typeAreas[owner][source.Area] = true
			}
			if row.Kind == "type" {
				matched = true
				for _, target := range source.GoSymbols {
					if declaration, exists := goAPI.Symbols[target]; exists && declaration.Kind == "type" {
						typeTargets[owner] = append(typeTargets[owner], target)
					}
				}
			} else {
				members := names.resolveMember(owner, row.Kind, source.typeName, row.Member)
				if len(members) == 1 {
					ref.key, matched = members[0], true
					row.Member = names.memberLabel(owner, row.Kind, ref.key)
				} else {
					if len(members) == 0 {
						row.Reason = "member was not resolved on the inventory type; selection, visibility, or baseline differences may be responsible"
						members = declarationMemberCandidates(typ, owner, row.Kind, row.Member)
					} else {
						row.Reason = fmt.Sprintf("member identity resolves to %d inventory declarations; the assessment was not linked", len(members))
					}
					for _, key := range members {
						row.Candidates = append(row.Candidates, qualifiedDeclarationLabel(names, declarationRef{owner: owner, kind: row.Kind, key: key}))
					}
				}
			}
			if matched {
				row.State = "linked"
				annotateReconciliationDeclaration(&row, typ, ref)
			}
		default:
			row.Reason = fmt.Sprintf("declaring type resolves to %d inventory types; the assessment was not linked", len(owners))
			for _, owner := range owners {
				row.Candidates = append(row.Candidates, qualifiedDeclarationLabel(names, declarationRef{owner: owner, kind: "type"}))
			}
		}
		if row.Area == "" {
			row.Area = "unclassified"
		}
		row.Dotnet = row.Type
		if row.Member != "" {
			row.Dotnet += "." + row.Member
		}
		// Do not compact labels: distinct canonical declarations can have the
		// same readable spelling, and that ambiguity must remain visible.
		slices.Sort(row.Candidates)
		if matched {
			if row.Status == "" {
				// A grouping-only source row contributes area, not an assessment.
				consumed[i] = true
			} else {
				claims[ref] = append(claims[ref], i)
			}
		}
		if row.Status != "" {
			dotnetCommit, goCommit := report.Baseline.DotnetCommit, report.Baseline.GoCommit
			if row.Review != "" {
				review := report.Reviews[row.Review]
				dotnetCommit, goCommit = review.DotnetCommit, review.GoCommit
			}
			row.ReviewSourceChanged = reconciliationCommitChanged(dotnetCommit, sourceCommits[row.Assembly])
			row.ReviewGoChanged = reconciliationCommitChanged(goCommit, goAPI.Commit)
			checkReconciliationGoTargets(&row, goAPI, goPackages, allGoPackages)
		}
		sourceRows[i] = row
	}

	addDeclaration := func(ref declarationRef) {
		result.InventoryDeclarations++
		indices := claims[ref]
		if len(indices) == 1 {
			i := indices[0]
			row := sourceRows[i]
			if report.Mappings[i].Area == "" {
				row.Area = reconciliationArea(row.Namespace, typeAreas[ref.owner])
			}
			result.Rows = append(result.Rows, row)
			consumed[i] = true
			return
		}
		for _, i := range indices {
			sourceRows[i].State = "needs-reconciliation"
			sourceRows[i].Reason = fmt.Sprintf("%d catalog mappings resolve to the same inventory declaration; no unique assessment was linked", len(indices))
		}
		namespace, typeName := names.typeLabel(ref.owner)
		row := reconciliationRow{
			Namespace: namespace, Type: typeName, Dotnet: typeName,
			Assembly: inv.Types[ref.owner].Assembly, Kind: ref.kind,
			Area:  reconciliationArea(namespace, typeAreas[ref.owner]),
			State: "unreviewed", GoSymbols: []string{},
		}
		if ref.kind != "type" {
			row.Member = names.memberLabel(ref.owner, ref.kind, ref.key)
			row.Dotnet += "." + row.Member
		}
		annotateReconciliationDeclaration(&row, inv.Types[ref.owner], ref)
		if !row.LanguageMachinery {
			row.SuggestedGo = suggestGoTargets(row, typeTargets[ref.owner], goAPI)
		}
		result.Rows = append(result.Rows, row)
	}
	for _, owner := range slices.Sorted(maps.Keys(inv.Types)) {
		// Member-only catalog groups still leave a real, unreviewed type row.
		addDeclaration(declarationRef{owner: owner, kind: "type"})
		for _, kind := range []string{"constant", "constructor", "event", "field", "method", "property"} {
			for _, key := range declarationMemberKeys(inv.Types[owner], kind) {
				addDeclaration(declarationRef{owner: owner, kind: kind, key: key})
			}
		}
	}
	// Unresolved and conflicting source assessments never consume an
	// unreviewed declaration. Preserve their source order when labels tie.
	for i, row := range sourceRows {
		if !consumed[i] {
			result.Rows = append(result.Rows, row)
		}
	}
	slices.SortStableFunc(result.Rows, func(a, b reconciliationRow) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Type, b.Type),
			cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Member, b.Member))
	})
	summarizeReconciliation(&result)
	return result
}

func summarizeReconciliation(report *reconciliationReport) {
	report.Counts = make(map[string]int, len(reconciliationStates))
	for _, state := range reconciliationStates {
		report.Counts[state] = 0
	}
	report.ByArea = make(map[string]map[string]int)
	report.AssessedDeclarations = 0
	for _, row := range report.Rows {
		report.Counts[row.State]++
		if report.ByArea[row.Area] == nil {
			report.ByArea[row.Area] = make(map[string]int, len(reconciliationStates))
			for _, state := range reconciliationStates {
				report.ByArea[row.Area][state] = 0
			}
		}
		report.ByArea[row.Area][row.State]++
		if row.Status != "" {
			report.AssessedDeclarations++
		}
	}
}

func declarationMemberKeys(typ declarationType, kind string) []string {
	switch kind {
	case "constructor":
		return slices.Sorted(maps.Keys(typ.Constructors))
	case "method":
		return slices.Sorted(maps.Keys(typ.Methods))
	case "property":
		return slices.Sorted(maps.Keys(typ.Properties))
	case "event":
		return slices.Sorted(maps.Keys(typ.Events))
	case "field":
		return slices.Sorted(maps.Keys(typ.Fields))
	case "constant":
		return slices.Sorted(maps.Keys(typ.Constants))
	default:
		return nil
	}
}

// These are names-only suggestions, used only after identity resolution fails.
func declarationMemberCandidates(typ declarationType, owner, kind, member string) []string {
	signature, ok := dnReadSignature(member)
	if !ok {
		return nil
	}
	name := signature.name.name
	if kind == "constructor" && name == dnConstructor(owner) {
		name = ".ctor"
	}
	var candidates []string
	for _, key := range declarationMemberKeys(typ, kind) {
		candidate, ok := dnReadSignature(key)
		if ok && candidate.name.name == name {
			candidates = append(candidates, key)
		}
	}
	return candidates
}

func qualifiedDeclarationLabel(names *dotnetNames, ref declarationRef) string {
	namespace, label := names.typeLabel(ref.owner)
	if namespace != "" {
		label = namespace + "." + label
	}
	if ref.kind != "type" {
		label += "." + names.memberLabel(ref.owner, ref.kind, ref.key)
	}
	return label
}

func declarationIncludesNamespace(namespace string, prefixes []string) bool {
	return len(prefixes) == 0 || slices.ContainsFunc(prefixes, func(prefix string) bool {
		return namespace == prefix || strings.HasPrefix(namespace, prefix+".")
	})
}

func reconciliationArea(namespace string, known map[string]bool) string {
	if len(known) == 1 {
		for area := range known {
			return area
		}
	}
	switch {
	case namespace == "Microsoft.Agents.AI.Workflows" || strings.HasPrefix(namespace, "Microsoft.Agents.AI.Workflows."):
		return "workflows"
	case namespace == "Microsoft.Agents.AI" || namespace == "Microsoft.Agents.AI.Compaction" || strings.HasPrefix(namespace, "Microsoft.Agents.AI.Compaction."):
		return "agents"
	default:
		return "unclassified"
	}
}

func annotateReconciliationDeclaration(row *reconciliationRow, typ declarationType, ref declarationRef) {
	attributes := typ.Attributes
	method := false
	switch ref.kind {
	case "constructor":
		attributes, method = typ.Constructors[ref.key].Attributes, true
	case "method":
		attributes, method = typ.Methods[ref.key].Attributes, true
	case "property":
		attributes = typ.Properties[ref.key].Attributes
	case "event":
		attributes = typ.Events[ref.key].Attributes
	case "field":
		attributes = typ.Fields[ref.key].Attributes
	case "constant":
		attributes = typ.Constants[ref.key].Attributes
	}
	row.Experimental = typ.Attributes.Experimental
	if attributes.Experimental != "" {
		row.Experimental = attributes.Experimental
	}
	row.CompilerGenerated = method && attributes.CompilerGenerated
	name, _, _ := strings.Cut(ref.key, "(")
	row.LanguageMachinery = row.CompilerGenerated || typ.Kind == "delegate" &&
		(ref.kind == "constructor" || ref.kind == "method" && (name == "BeginInvoke" || name == "EndInvoke"))
}

func checkReconciliationGoTargets(row *reconciliationRow, goAPI goInventory, packages map[string]bool, allGoPackages bool) {
	for _, target := range row.GoSymbols {
		if _, exists := goAPI.Symbols[target]; exists {
			continue
		}
		pkg, _, qualified := strings.Cut(target, ".")
		if !qualified || pkg == "" || packages[pkg] || allGoPackages {
			row.InvalidGoTargets = append(row.InvalidGoTargets, target)
		} else {
			row.UnindexedGoTargets = append(row.UnindexedGoTargets, target)
		}
	}
	// Keep .NET resolution failures primary, but retain Go issues on those rows.
	if row.State != "linked" {
		return
	}
	switch {
	case len(row.InvalidGoTargets) != 0:
		row.State = "invalid-go-target"
		row.Reason = "one or more recorded Go targets do not exist in the selected Go API"
	case len(row.UnindexedGoTargets) != 0:
		row.State = "go-outside-scope"
		row.Reason = "one or more recorded Go target packages were not indexed; those targets have not been validated"
	}
}

func declarationSourceCommit(inv declarationInventory, assembly string) string {
	info, exists := inv.Assemblies[assembly]
	if !exists {
		return ""
	}
	if _, commit, found := strings.Cut(info.InformationalVersion, "+"); found && reconciliationCommitID(commit) {
		return strings.ToLower(commit)
	}
	var commit string
	for _, pkg := range inv.Packages {
		if !slices.Contains(pkg.Assemblies, assembly) || !reconciliationCommitID(pkg.Commit) {
			continue
		}
		if commit != "" && !strings.EqualFold(commit, pkg.Commit) {
			return "" // Conflicting package provenance is not a known baseline.
		}
		commit = strings.ToLower(pkg.Commit)
	}
	return commit
}

func reconciliationCommitID(value string) bool {
	// Build metadata and abbreviated revisions are not pinned commit IDs.
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func reconciliationCommitChanged(recorded, current string) *bool {
	if recorded == "" || current == "" {
		return nil
	}
	changed := !strings.EqualFold(recorded, current)
	return &changed
}

// These candidates only narrow the review search. They never set a mapping
// status, consume a declaration, or establish semantic equivalence.
func suggestGoTargets(row reconciliationRow, owners []string, api goInventory) []string {
	name := row.Type
	if row.Kind != "type" {
		name = row.Member
	}
	name, _, _ = strings.Cut(name, "(")
	name, _, _ = strings.Cut(name, "<")
	name = strings.TrimSuffix(name, "Async")
	var targets []string
	if row.Kind == "type" {
		for target, symbol := range api.Symbols {
			if symbol.Kind == "type" && strings.EqualFold(target[strings.LastIndexByte(target, '.')+1:], name) {
				targets = append(targets, target)
			}
		}
	} else if row.Kind != "constructor" {
		for target := range api.Symbols {
			for _, owner := range owners {
				if member, found := strings.CutPrefix(target, owner+"."); found && strings.EqualFold(member, name) {
					targets = append(targets, target)
				}
			}
		}
	}
	slices.Sort(targets)
	return slices.Compact(targets)
}
