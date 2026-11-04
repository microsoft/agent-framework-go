// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
)

type auditRevision struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type auditItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Subject   string         `json:"subject"`
	Parents   []string       `json:"parents,omitempty"`
	Dotnet    *auditRevision `json:"dotnet,omitempty"`
	Commit    string         `json:"commit,omitempty"`
	StartLine int            `json:"start_line,omitempty"`
	EndLine   int            `json:"end_line,omitempty"`
}

type auditPlan struct {
	Version       int             `json:"schema_version"`
	GoRepository  string          `json:"go_repository"`
	Head          string          `json:"head"`
	OldHead       string          `json:"old_head"`
	DotnetSources []auditRevision `json:"dotnet_sources"`
	RecentCommits []string        `json:"recent_commits"`
	Items         []auditItem     `json:"items"`
}

type auditEvidence struct {
	auditRevision
	Path string `json:"path"`
	Line int    `json:"line"`
}

type auditResult struct {
	ID                 string          `json:"id"`
	Subject            string          `json:"subject"`
	Outcome            string          `json:"outcome"`
	Reason             string          `json:"reason"`
	Evidence           []auditEvidence `json:"evidence"`
	ImplementingCommit string          `json:"implementing_commit,omitempty"`
}

type auditCoverage struct {
	Items map[string]int `json:"items"`
}

func runAudit(command string, args []string, out, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("symbolmap "+command, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	var options reportOptions
	var changesFile, patchFile, planFile, planHash, resultsFile string
	type requiredFlag struct {
		name  string
		value *string
	}
	var required []requiredFlag
	if command == "audit-plan" {
		addGoIndexFlags(flags, &options)
		flags.StringVar(&options.file, "file", defaultCatalogFile, "catalog path")
		flags.StringVar(&changesFile, "changes", "", "complete changes report (required)")
		flags.StringVar(&patchFile, "patch", "", "complete recent git log --patch output (required)")
		required = []requiredFlag{{"changes", &changesFile}, {"patch", &patchFile}}
	} else {
		flags.StringVar(&planFile, "plan", "", "original audit plan (required)")
		flags.StringVar(&planHash, "plan-sha256", "", "SHA-256 recorded before agent execution (required)")
		flags.StringVar(&resultsFile, "results", "", "item receipts as a JSON stream (required)")
		required = []requiredFlag{{"plan", &planFile}, {"plan-sha256", &planHash}, {"results", &resultsFile}}
	}
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	for _, field := range required {
		if strings.TrimSpace(*field.value) == "" {
			return fmt.Errorf("%s requires -%s", command, field.name)
		}
	}
	if command == "audit-check" {
		return checkAudit(out, planFile, planHash, resultsFile)
	}
	catalog, err := loadCatalog(options.file)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(changesFile)
	if err != nil {
		return err
	}
	var changes goChangesReport
	if err := decodeAuditJSON(data, &changes); err != nil {
		return fmt.Errorf("%s: %w", changesFile, err)
	}
	if changes.SymbolFilter == nil || *changes.SymbolFilter != "" ||
		changes.Page.Offset != 0 || changes.Page.Total != len(changes.Changes) ||
		changes.Page.Returned != len(changes.Changes) || changes.Page.NextOffset != nil ||
		changes.Compatible+changes.Incompatible != len(changes.Changes) {
		return errors.New("audit-plan requires a complete, unfiltered changes report")
	}
	api, err := indexGo(options.goRoot, options.goPatterns, options.tags)
	if err != nil {
		return err
	}
	if api.Module != catalog.Baseline.GoModule || changes.Old.Module != api.Module ||
		changes.New.Module != api.Module || api.Commit != changes.New.Commit ||
		api.GOOS != changes.New.GOOS || api.GOARCH != changes.New.GOARCH ||
		api.GoVersion != changes.New.GoVersion || api.CGOEnabled != changes.New.CGOEnabled ||
		api.BuildTags != changes.New.BuildTags || !slices.Equal(api.Packages, changes.New.Packages) ||
		len(api.Symbols) != changes.New.ExportedSymbols {
		return errors.New("changes report does not match the current Go index")
	}
	patch, err := os.ReadFile(patchFile)
	if err != nil {
		return err
	}
	plan, err := makeAuditPlan(catalog, changes, api, string(patch))
	if err != nil {
		return err
	}
	return writeReportJSON(out, plan)
}

func makeAuditPlan(catalog symbolcatalog.Catalog, changes goChangesReport, api goInventory, patch string) (auditPlan, error) {
	plan := auditPlan{
		Version: 1, GoRepository: catalog.Baseline.GoRepository,
		Head: changes.New.Commit, OldHead: changes.Old.Commit,
		Items: []auditItem{}, RecentCommits: []string{},
	}
	if strings.TrimSpace(plan.GoRepository) == "" || !reconciliationCommitID(plan.Head) || !reconciliationCommitID(plan.OldHead) {
		return auditPlan{}, errors.New("audit-plan requires full Go source commits and a repository")
	}
	baseline := auditRevision{catalog.Baseline.DotnetRepository, catalog.Baseline.DotnetCommit}
	if !auditSourceRevision(baseline) {
		return auditPlan{}, errors.New("audit-plan requires a pinned .NET baseline")
	}
	plan.DotnetSources = []auditRevision{baseline}
	assemblySources := make(map[string]auditRevision)
	if catalog.Dotnet != nil {
		inventory := symbolcatalog.Inventory{Assemblies: catalog.Dotnet.Assemblies, Packages: catalog.Dotnet.Packages}
		for _, assembly := range slices.Sorted(maps.Keys(inventory.Assemblies)) {
			source := auditRevision{baseline.Repository, declarationSourceCommit(inventory, assembly)}
			var repository string
			for _, name := range slices.Sorted(maps.Keys(inventory.Packages)) {
				pkg := inventory.Packages[name]
				if !slices.Contains(pkg.Assemblies, assembly) || pkg.Repository == "" {
					continue
				}
				if repository != "" && repository != pkg.Repository {
					return auditPlan{}, fmt.Errorf("assembly %q has conflicting source repositories", assembly)
				}
				repository = pkg.Repository
				source.Repository = repository
			}
			if !auditSourceRevision(source) {
				return auditPlan{}, fmt.Errorf("assembly %q has no pinned source revision", assembly)
			}
			plan.DotnetSources = append(plan.DotnetSources, source)
			assemblySources[assembly] = source
		}
	}
	slices.SortFunc(plan.DotnetSources, func(a, b auditRevision) int {
		if order := strings.Compare(a.Repository, b.Repository); order != 0 {
			return order
		}
		return strings.Compare(a.Commit, b.Commit)
	})
	plan.DotnetSources = slices.Compact(plan.DotnetSources)

	members := make(map[string]auditItem)
	for index, change := range changes.Changes {
		id := "api:" + strconv.Itoa(index)
		plan.Items = append(plan.Items, auditItem{ID: id, Kind: "api", Subject: change.Message})
		name, _, _ := strings.Cut(change.Message, ": ")
		name = strings.TrimPrefix(name, "./")
		if !strings.HasSuffix(change.Message, ": added") {
			continue
		}
		var prefix string
		if pkg, ok := strings.CutPrefix(name, "package "); ok &&
			(pkg == api.Module || strings.HasPrefix(pkg, api.Module+"/")) {
			prefix = goIndexPackageName(pkg, api.Module) + "."
		} else if api.Symbols[name].Kind == "type" {
			prefix = name + "."
		} else {
			continue
		}
		for symbol := range api.Symbols {
			if strings.HasPrefix(symbol, prefix) {
				item := members[symbol]
				item.ID, item.Kind, item.Subject = "member:"+symbol, "member", symbol
				item.Parents = append(item.Parents, id)
				members[symbol] = item
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(members)) {
		plan.Items = append(plan.Items, members[name])
	}
	behaviorItems, commits, err := auditPatchItems(patch)
	if err != nil {
		return auditPlan{}, err
	}
	plan.RecentCommits = commits
	plan.Items = append(plan.Items, behaviorItems...)
	index := 0
	for _, mapping := range flattenMappings(catalog) {
		if mapping.Unreviewed {
			continue
		}
		source, ok := assemblySources[mapping.Assembly]
		if !ok || mapping.unavailable || catalog.Dotnet == nil || !declarationIncludesNamespace(mapping.Namespace, catalog.Dotnet.Selection.Namespaces) {
			source = baseline
		}
		plan.Items = append(plan.Items, auditItem{
			ID: "assessment:" + strconv.Itoa(index), Kind: "assessment", Subject: mapping.Namespace + "." + mapping.Dotnet,
			Dotnet: &source,
		})
		index++
	}
	for _, name := range slices.Sorted(maps.Keys(catalog.GoOnly)) {
		plan.Items = append(plan.Items, auditItem{ID: "go-only:" + name, Kind: "go-only", Subject: name})
	}
	return plan, nil
}

func auditPatchItems(patch string) ([]auditItem, []string, error) {
	items := []auditItem{}
	commits := []string{}
	seen := make(map[string]bool)
	var commit string
	var active bool
	lines := strings.Split(patch, "\n")
	finish := func(line int) {
		if active {
			items[len(items)-1].EndLine = line
			active = false
		}
	}
	for line, text := range lines {
		text = strings.TrimSuffix(text, "\r")
		if next, ok := strings.CutPrefix(text, "commit "); ok {
			finish(line)
			if !reconciliationCommitID(next) {
				return nil, nil, fmt.Errorf("invalid patch commit at line %d", line+1)
			}
			commit = next
			commits = append(commits, commit)
		} else if strings.HasPrefix(text, "diff --git ") {
			finish(line)
			if commit == "" || strings.TrimSpace(strings.TrimPrefix(text, "diff --git ")) == "" {
				return nil, nil, fmt.Errorf("patch diff has no commit or file at line %d", line+1)
			}
			subject := commit + " " + text
			if seen[subject] {
				return nil, nil, fmt.Errorf("duplicate commit/file patch: %s", subject)
			}
			seen[subject] = true
			items = append(items, auditItem{
				ID: "behavior:" + strconv.Itoa(len(items)), Kind: "behavior", Subject: subject, Commit: commit, StartLine: line + 1,
			})
			active = true
		}
	}
	finish(len(lines))
	if strings.TrimSpace(patch) != "" && len(commits) == 0 {
		return nil, nil, errors.New("recent patch has no Git commits")
	}
	slices.Sort(commits)
	return items, slices.Compact(commits), nil
}

func checkAudit(out io.Writer, planFile, planHash, resultsFile string) error {
	expected, err := hex.DecodeString(planHash)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("-plan-sha256 must be a SHA-256 digest")
	}
	data, err := os.ReadFile(planFile)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if !bytes.Equal(expected, digest[:]) {
		return errors.New("audit plan differs from the pre-agent SHA-256")
	}
	var plan auditPlan
	if err := decodeAuditJSON(data, &plan); err != nil {
		return fmt.Errorf("%s: %w", planFile, err)
	}
	if plan.Version != 1 {
		return fmt.Errorf("unsupported audit plan schema %d", plan.Version)
	}
	expectedItems := make(map[string]auditItem)
	for _, item := range plan.Items {
		if _, exists := expectedItems[item.ID]; exists || item.ID == "" || item.Subject == "" {
			return fmt.Errorf("invalid or duplicate planned item %q", item.ID)
		}
		switch item.Kind {
		case "api", "member", "behavior", "assessment", "go-only":
		default:
			return fmt.Errorf("unknown audit kind %q", item.Kind)
		}
		expectedItems[item.ID] = item
	}
	results, err := os.ReadFile(resultsFile)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(results))
	decoder.DisallowUnknownFields()
	seenItems := make(map[string]bool)
	coverage := auditCoverage{Items: map[string]int{"api": 0, "member": 0, "behavior": 0, "assessment": 0, "go-only": 0}}
	for {
		var receipt auditResult
		if err := decoder.Decode(&receipt); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("decode item receipt at byte %d: %w", decoder.InputOffset(), err)
		}
		item, ok := expectedItems[receipt.ID]
		if !ok {
			return fmt.Errorf("unassigned audit item %q", receipt.ID)
		}
		if seenItems[receipt.ID] {
			return fmt.Errorf("duplicate audit item %q", receipt.ID)
		}
		if err := validateAuditResult(plan, item, receipt); err != nil {
			return fmt.Errorf("audit item %q: %w", receipt.ID, err)
		}
		seenItems[receipt.ID] = true
		coverage.Items[item.Kind]++
	}
	if len(seenItems) != len(expectedItems) {
		var missing []string
		for _, id := range slices.Sorted(maps.Keys(expectedItems)) {
			if !seenItems[id] {
				missing = append(missing, id)
			}
		}
		return fmt.Errorf("audit incomplete: %d missing items: %s", len(missing), strings.Join(missing, ", "))
	}
	return writeReportJSON(out, coverage)
}

func validateAuditResult(plan auditPlan, item auditItem, result auditResult) error {
	if result.Subject != item.Subject {
		return errors.New("subject does not match the assigned identity")
	}
	switch result.Outcome {
	case "no-change", "correction", "addition", "go-specific", "scope-limitation":
	default:
		return fmt.Errorf("unresolved or invalid outcome %q", result.Outcome)
	}
	if strings.TrimSpace(result.Reason) == "" {
		return errors.New("source-backed reason is required")
	}
	if result.Outcome == "addition" && !slices.Contains(plan.RecentCommits, result.ImplementingCommit) {
		return errors.New("new assessment requires a recent implementing commit")
	}
	goCommit := plan.Head
	if item.Kind == "behavior" {
		goCommit = item.Commit
	}
	var goEvidence, dotnetEvidence bool
	for _, evidence := range result.Evidence {
		if evidence.Line < 1 || evidence.Path == "." || !fs.ValidPath(evidence.Path) || strings.ContainsAny(evidence.Path, "\\:\x00") {
			return errors.New("evidence requires a repository-relative path and a positive line")
		}
		source := evidence.auditRevision
		if source.Repository == plan.GoRepository {
			if source.Commit != plan.Head && source.Commit != plan.OldHead && !slices.Contains(plan.RecentCommits, source.Commit) {
				return fmt.Errorf("unrecognized Go evidence revision %q", source.Commit)
			}
			goEvidence = goEvidence || source.Commit == goCommit
		} else {
			if !slices.Contains(plan.DotnetSources, source) {
				return fmt.Errorf("unrecognized .NET evidence revision %q at %q", source.Commit, source.Repository)
			}
			dotnetEvidence = dotnetEvidence || item.Dotnet == nil || source == *item.Dotnet
		}
	}
	if !goEvidence {
		return fmt.Errorf("evidence at Go commit %s is required", goCommit)
	}
	if (item.Kind == "assessment" || item.Kind == "api" || item.Kind == "member" || result.Outcome != "no-change") && !dotnetEvidence {
		return errors.New("evidence at the applicable pinned .NET revision is required")
	}
	return nil
}

func auditSourceRevision(source auditRevision) bool {
	return strings.TrimSpace(source.Repository) != "" && reconciliationCommitID(source.Commit)
}

func decodeAuditJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected a single JSON document")
	}
	return nil
}
