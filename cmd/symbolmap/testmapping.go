// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/cmd/internal/testinventory"
)

// Tests pair exact assembly/type/name keys with unique Go test functions.
type testMappings map[string]map[string]map[string]string

type testRef struct {
	assembly string
	owner    string
	method   string
}

// Unpaired Go tests record whether source review has been completed.
type goTestQueue struct {
	Unreviewed []string `json:"unreviewed"`
	Reviewed   []string `json:"reviewed,omitempty"`
}

func (c catalog) validateGoTestQueue() error {
	if c.GoTests == nil {
		return nil
	}
	if c.GoTests.Unreviewed == nil {
		return errors.New("go_tests: unreviewed must be a non-null array")
	}
	seen := make(map[string]string, len(c.GoTests.Unreviewed)+len(c.GoTests.Reviewed))
	for _, group := range []struct {
		state string
		names []string
	}{
		{"unreviewed", c.GoTests.Unreviewed},
		{"reviewed", c.GoTests.Reviewed},
	} {
		for _, name := range group.names {
			if !validGoTestTarget(name) {
				return fmt.Errorf("go_tests: invalid qualified Go test function %q", name)
			}
			if previous, exists := seen[name]; exists {
				if previous == group.state {
					return fmt.Errorf("go_tests: duplicate %s Go test %q", group.state, name)
				}
				return fmt.Errorf("go_tests: Go test %q appears in both unreviewed and reviewed", name)
			}
			seen[name] = group.state
		}
	}
	return nil
}

// An absent section is compatible with old catalogs; an explicit null is not
// an empty object.
func (m *testMappings) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("tests must be a non-null object")
	}
	type plain testMappings
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("tests: %w", err)
	}
	*m = testMappings(decoded)
	return nil
}

func (c catalog) flattenTests() (map[testRef]string, error) {
	pairs := make(map[testRef]string)
	goOwners := make(map[string]string)
	for _, assembly := range slices.Sorted(maps.Keys(c.Tests)) {
		types := c.Tests[assembly]
		if !testAssemblyPattern.MatchString(assembly) || len(types) == 0 {
			return nil, fmt.Errorf("tests: invalid or empty assembly %q", assembly)
		}
		for _, owner := range slices.Sorted(maps.Keys(types)) {
			methods := types[owner]
			if !validTestType(owner) || len(methods) == 0 {
				return nil, fmt.Errorf("tests %s: invalid or empty declaring type %q", assembly, owner)
			}
			for _, method := range slices.Sorted(maps.Keys(methods)) {
				if !namePattern.MatchString(method) {
					return nil, fmt.Errorf("tests %s %s: invalid test name %q", assembly, owner, method)
				}
				target := methods[method]
				label := assembly + " " + owner + "." + method
				if !validGoTestTarget(target) {
					return nil, fmt.Errorf("tests %s: invalid qualified Go test function %q", label, target)
				}
				if previous, exists := goOwners[target]; exists {
					return nil, fmt.Errorf("tests %s: Go test %q is already mapped to %s; test mappings must be one-to-one", label, target, previous)
				}
				goOwners[target] = label
				pairs[testRef{assembly: assembly, owner: owner, method: method}] = target
			}
		}
	}
	if c.GoTests != nil {
		for _, target := range c.GoTests.Unreviewed {
			if owner, exists := goOwners[target]; exists {
				return nil, fmt.Errorf("go_tests: unreviewed Go test %q is already mapped to %s", target, owner)
			}
		}
		for _, target := range c.GoTests.Reviewed {
			if owner, exists := goOwners[target]; exists {
				return nil, fmt.Errorf("go_tests: reviewed Go test %q is already mapped to %s", target, owner)
			}
		}
	}
	return pairs, nil
}

func decodeTestInventory(data []byte) (*testinventory.Inventory, error) {
	if len(data) == 0 {
		return nil, nil
	}
	// API metadata still accepts its optional fields. Test records have their
	// own identity format and must not silently accept a different leaf schema.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var inv *testinventory.Inventory
	if err := decoder.Decode(&inv); err != nil {
		return nil, fmt.Errorf("test inventory: %w", err)
	}
	if inv == nil {
		return nil, errors.New("test inventory tests must be a non-null object")
	}
	if inv.IdentityFormat != testinventory.IdentityFormat {
		return nil, fmt.Errorf("unsupported test inventory identity_format %q", inv.IdentityFormat)
	}
	if len(inv.Assemblies) == 0 {
		return nil, errors.New("test inventory assemblies must not be empty")
	}
	for _, name := range slices.Sorted(maps.Keys(inv.Assemblies)) {
		assembly := inv.Assemblies[name]
		if !testAssemblyPattern.MatchString(name) || len(assembly.Types) == 0 {
			return nil, fmt.Errorf("test inventory: invalid or empty assembly %q", name)
		}
		if assembly.Commit != "" && !reconciliationCommitID(assembly.Commit) {
			return nil, fmt.Errorf("test inventory %s: commit must be a full source revision", name)
		}
		if len(assembly.SHA256) != 64 || strings.Trim(assembly.SHA256, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("test inventory %s: assembly SHA256 is required", name)
		}
		for _, owner := range slices.Sorted(maps.Keys(assembly.Types)) {
			methods := assembly.Types[owner]
			if !validTestType(owner) || len(methods) == 0 {
				return nil, fmt.Errorf("test inventory %s: invalid or empty metadata type %q", name, owner)
			}
			seen := make(map[string]bool, len(methods))
			for _, method := range methods {
				if !namePattern.MatchString(method) {
					return nil, fmt.Errorf("test inventory %s %s: invalid test name %q", name, owner, method)
				}
				if seen[method] {
					return nil, fmt.Errorf("test inventory %s %s: duplicate test name %q", name, owner, method)
				}
				seen[method] = true
			}
		}
	}
	return inv, nil
}

var testAssemblyPattern = regexp.MustCompile(`^[\p{L}\p{Nd}_][\p{L}\p{Nd}_.-]*$`)

func validTestType(value string) bool {
	return value != "" && dnToken.FindString(value) == value
}
