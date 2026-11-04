// Copyright (c) Microsoft. All rights reserved.

package symbolcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	identifier = `[\p{L}_][\p{L}\p{Nd}_]*`
	dotnetName = identifier + "(?:`[1-9][0-9]*|<[^()]+>)?"
)

var (
	areas               = []string{"agents", "messages", "tools", "providers", "hosting", "operations", "workflows", "unclassified"}
	statuses            = []string{"mapped", "adapted", "partial", "unmapped", "intentional"}
	shaPattern          = regexp.MustCompile(`^[0-9a-f]{40}$`)
	modulePattern       = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_-][A-Za-z0-9_.-]*)*$`)
	dotnetTypePattern   = regexp.MustCompile(`^` + dotnetName + `(?:\.` + dotnetName + `)+$`)
	namespacePattern    = regexp.MustCompile(`^` + identifier + `(?:\.` + identifier + `)*$`)
	namePattern         = regexp.MustCompile(`^` + identifier + `$`)
	callablePattern     = regexp.MustCompile(`^` + dotnetName + `$`)
	goTargetPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*\.\p{Lu}[\p{L}\p{Nd}_]*(?:\.\p{Lu}[\p{L}\p{Nd}_]*)?$`)
	testAssemblyPattern = regexp.MustCompile(`^[\p{L}\p{Nd}_][\p{L}\p{Nd}_.-]*$`)
)

// Decode checks the whole document before decoding maps, including duplicate
// keys and unknown fields. Validation is independent of report filters.
func Decode(data []byte) (Catalog, error) {
	var c Catalog
	if err := checkDocument(data); err != nil {
		return c, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func checkDocument(data []byte) error {
	if root := bytes.TrimSpace(data); len(root) == 0 || root[0] != '{' {
		return errors.New("expected a single JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := checkJSONKeys(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("expected a single JSON object")
	}
	return nil
}

func checkJSONKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = true
			if err := checkJSONKeys(decoder); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for decoder.More() {
			if err := checkJSONKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = decoder.Token()
	return err
}

func (c Catalog) Validate() error {
	if c.SchemaVersion == nil {
		return errors.New("schema_version is required")
	}
	if *c.SchemaVersion != 0 {
		return fmt.Errorf("unsupported schema_version %d", *c.SchemaVersion)
	}
	b := c.Baseline
	if !validDate(b.CheckedAt) || !shaPattern.MatchString(b.DotnetCommit) || !shaPattern.MatchString(b.GoCommit) {
		return errors.New("baseline requires valid checked_at and pinned dotnet_commit/go_commit")
	}
	if !validURL(b.DotnetRepository) || !validURL(b.GoRepository) || !modulePattern.MatchString(b.GoModule) || b.InventoryComplete == nil || strings.TrimSpace(b.Scope) == "" {
		return errors.New("baseline requires repository URLs, go_module, inventory_complete, and a nonempty scope")
	}
	for _, symbol := range slices.Sorted(maps.Keys(c.GoOnly)) {
		assessment := c.GoOnly[symbol]
		if !goTargetPattern.MatchString(symbol) {
			return fmt.Errorf("go_only: invalid qualified Go symbol %q", symbol)
		}
		if strings.TrimSpace(assessment.Note) == "" {
			return fmt.Errorf("go_only %s: note must not be empty", symbol)
		}
	}
	if err := c.validateTests(); err != nil {
		return err
	}
	if len(c.Namespaces) == 0 {
		return errors.New("namespaces must not be empty")
	}
	seenTypes, seenSymbols := make(map[string]bool), make(map[string]bool)
	for _, namespace := range slices.Sorted(maps.Keys(c.Namespaces)) {
		entries := c.Namespaces[namespace]
		if !namespacePattern.MatchString(namespace) || len(entries) == 0 {
			return fmt.Errorf("invalid or empty namespace %q", namespace)
		}
		for _, name := range slices.Sorted(maps.Keys(entries)) {
			entry, full := entries[name], namespace+"."+name
			if !dotnetTypePattern.MatchString(full) || !balancedAngles(full) {
				return fmt.Errorf("invalid namespace-qualified .NET type %q", full)
			}
			identity := symbolIdentity(full)
			if seenTypes[identity] {
				return fmt.Errorf("duplicate .NET type %q", full)
			}
			seenTypes[identity] = true
			if !slices.Contains(areas, entry.Area) {
				return fmt.Errorf("%s: invalid area %q", full, entry.Area)
			}
			if !slices.Contains([]string{"", "class", "interface", "struct", "enum", "delegate"}, entry.Kind) {
				return fmt.Errorf("%s: invalid type kind %q", full, entry.Kind)
			}
			if entry.Identity != "" && !ValidTypeName(namespace+"."+entry.Identity) {
				return fmt.Errorf("%s: invalid declaring type identity %q", full, entry.Identity)
			}
			validate := func(kind, symbol string, m Mapping) error {
				identity := symbolIdentity(symbol)
				if seenSymbols[identity] {
					return fmt.Errorf("duplicate .NET symbol %q", symbol)
				}
				seenSymbols[identity] = true
				if err := m.validate(symbol, kind); err != nil {
					return err
				}
				if m.Unreviewed && (!c.includesType(namespace, entry) || entry.Unavailable || m.Unavailable) {
					return fmt.Errorf("%s: unreviewed entries require a current extracted declaration", symbol)
				}
				for _, target := range m.GoSymbols {
					if _, exists := c.GoOnly[target]; exists {
						return fmt.Errorf("%s: Go symbol %q is also assessed as go_only", symbol, target)
					}
				}
				return nil
			}
			for _, group := range memberGroups(&entry) {
				for _, member := range slices.Sorted(maps.Keys(*group.members)) {
					if !validMember(member, group.kind) {
						return fmt.Errorf("%s: invalid %s key %q", full, group.kind, member)
					}
					if err := validate(group.kind, full+"."+member, (*group.members)[member]); err != nil {
						return err
					}
				}
			}
			if entry.Mapping != nil {
				if err := validate("type", full, *entry.Mapping); err != nil {
					return err
				}
			}
		}
	}
	if c.Dotnet != nil {
		_, err := c.Declarations()
		return err
	}
	return nil
}

func (m Mapping) validate(owner, kind string) error {
	if !slices.Contains(statuses, m.Status) {
		return fmt.Errorf("%s: invalid status %q", owner, m.Status)
	}
	if m.Unreviewed {
		if m.Status != "unmapped" || len(m.GoSymbols) != 0 || m.Note != "" || m.Go != nil && *m.Go != "" {
			return fmt.Errorf("%s: unreviewed entries must be unmapped without counterparts or notes", owner)
		}
	} else if m.Status != "mapped" && strings.TrimSpace(m.Note) == "" {
		return fmt.Errorf("%s: note must not be empty", owner)
	}
	if kind == "type" && (m.Identity != "" || m.Experimental != "" || m.Generated || m.Unavailable) {
		return fmt.Errorf("%s: type declaration markers belong on its container", owner)
	}
	if m.Identity != "" {
		if _, ok := dnReadSignature(m.Identity); !ok {
			return fmt.Errorf("%s: invalid member identity %q", owner, m.Identity)
		}
	}
	if kind == "property" && m.Go != nil {
		return fmt.Errorf("%s: properties must not define Go examples", owner)
	}
	if kind != "property" && m.Go == nil {
		return fmt.Errorf("%s: go must be a non-null string", owner)
	}
	if m.GoSymbols == nil {
		return fmt.Errorf("%s: go_symbols must be a non-null array", owner)
	}
	if kind == "property" {
		if len(m.GoSymbols) == 0 && m.Status != "unmapped" && m.Status != "intentional" {
			return fmt.Errorf("%s: empty go_symbols requires unmapped or intentional status", owner)
		}
	} else if strings.TrimSpace(*m.Go) == "" {
		if m.Status != "unmapped" && m.Status != "intentional" {
			return fmt.Errorf("%s: empty go requires unmapped or intentional status", owner)
		}
		if len(m.GoSymbols) != 0 {
			return fmt.Errorf("%s: empty go requires empty go_symbols", owner)
		}
	} else {
		if strings.HasPrefix(strings.TrimSpace(*m.Go), "func(") {
			return fmt.Errorf("%s: go example must not use a synthetic outer function", owner)
		}
		if len(m.GoSymbols) == 0 {
			return fmt.Errorf("%s: nonempty go requires go_symbols", owner)
		}
		if _, _, err := ParseGoExample(*m.Go, false); err != nil {
			return fmt.Errorf("%s: invalid Go example: %w", owner, err)
		}
	}
	seen := make(map[string]bool)
	for _, target := range m.GoSymbols {
		if !goTargetPattern.MatchString(target) {
			return fmt.Errorf("%s: invalid qualified Go symbol %q", owner, target)
		}
		if seen[target] {
			return fmt.Errorf("%s: duplicate Go target %q", owner, target)
		}
		seen[target] = true
	}
	return nil
}

func validMember(value, kind string) bool {
	if kind != "method" && kind != "constructor" && kind != "property" {
		return namePattern.MatchString(value)
	}
	parts, ok := Split(value, "->")
	if !ok || len(parts) > 2 || len(parts) == 2 && strings.TrimSpace(parts[1]) == "" {
		return false
	}
	head := parts[0]
	if kind == "property" && namePattern.MatchString(head) {
		return true
	}
	if strings.HasPrefix(head, ".ctor(") {
		head = "Constructor" + strings.TrimPrefix(head, ".ctor")
	}
	if kind == "method" && strings.HasPrefix(head, "<Clone>$(") {
		head = "Clone" + strings.TrimPrefix(head, "<Clone>$")
	}
	head = strings.ReplaceAll(head, "``", "`")
	open := strings.IndexByte(head, '(')
	if open < 1 || !strings.HasSuffix(head, ")") || !callablePattern.MatchString(strings.TrimSpace(head[:open])) || !balancedAngles(head[:open]) {
		return false
	}
	depth := 0
	for i, r := range head[open:] {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && open+i != len(head)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func balancedAngles(value string) bool {
	depth := 0
	for _, r := range value {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func symbolIdentity(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}

func validDate(value string) bool {
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}

func validURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func validHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func validCommit(value string) bool {
	return (len(value) == 40 || len(value) == 64) && strings.Trim(strings.ToLower(value), "0123456789abcdef") == ""
}

// ValidGoTestTarget checks the directory-qualified static Go test identity.
func ValidGoTestTarget(target string) bool {
	separator := strings.LastIndexByte(target, '.')
	if separator < 1 {
		return false
	}
	name := target[separator+1:]
	if !token.IsIdentifier(name) || !strings.HasPrefix(name, "Test") {
		return false
	}
	if name != "Test" {
		r, _ := utf8.DecodeRuneInString(name[len("Test"):])
		if unicode.IsLower(r) {
			return false
		}
	}
	dir := target[:separator]
	return fs.ValidPath(dir) && !strings.ContainsAny(dir, "\\:") &&
		!strings.ContainsFunc(dir, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// ParseGoExample adds parsing boilerplate without supplying example inputs.
func ParseGoExample(code string, callStatement bool) (*token.FileSet, *ast.File, error) {
	if strings.TrimSpace(code) == "" {
		return nil, nil, errors.New("go example must not be empty")
	}
	parse := func(body string) (*token.FileSet, *ast.File, error) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "example.go", "package example\n"+body+"\n", parser.AllErrors)
		return fset, file, err
	}
	fset, file, err := parse(code)
	if err == nil {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				if decl.Tok == token.IMPORT {
					return nil, nil, errors.New("go examples must not declare imports")
				}
			case *ast.FuncDecl:
				if decl.Recv == nil && decl.Name.Name == "init" {
					return nil, nil, errors.New("go examples must not declare init functions")
				}
			}
		}
		if len(file.Decls) == 0 {
			return nil, nil, errors.New("go example must contain code, not only comments")
		}
		return fset, file, nil
	}
	if expression, err := parser.ParseExpr(code); err == nil {
		if _, ok := expression.(*ast.CallExpr); ok && callStatement {
			return parse("func example() {\n" + code + "\n}")
		}
		return parse("var _ = " + code)
	}
	return parse("func example() {\n" + code + "\n}")
}
