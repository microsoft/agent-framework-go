// Copyright (c) Microsoft. All rights reserved.

package symbolcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

func (m *TestMappings) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("tests must be a non-null object")
	}
	type plain TestMappings
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("tests: %w", err)
	}
	*m = TestMappings(decoded)
	return nil
}

func (c Catalog) validateTests() error {
	goOwners := make(map[string]string)
	for _, assembly := range slices.Sorted(maps.Keys(c.Tests)) {
		types := c.Tests[assembly]
		if !testAssemblyPattern.MatchString(assembly) || len(types) == 0 {
			return fmt.Errorf("tests: invalid or empty assembly %q", assembly)
		}
		for _, owner := range slices.Sorted(maps.Keys(types)) {
			methods := types[owner]
			if !ValidTypeName(owner) || len(methods) == 0 {
				return fmt.Errorf("tests %s: invalid or empty declaring type %q", assembly, owner)
			}
			for _, method := range slices.Sorted(maps.Keys(methods)) {
				if !namePattern.MatchString(method) {
					return fmt.Errorf("tests %s %s: invalid test name %q", assembly, owner, method)
				}
				target := methods[method]
				if target == nil {
					continue
				}
				label := assembly + " " + owner + "." + method
				if !ValidGoTestTarget(*target) {
					return fmt.Errorf("tests %s: invalid qualified Go test function %q", label, *target)
				}
				if previous, exists := goOwners[*target]; exists {
					return fmt.Errorf("tests %s: Go test %q is already mapped to %s; test mappings must be one-to-one", label, *target, previous)
				}
				goOwners[*target] = label
			}
		}
	}
	return nil
}
