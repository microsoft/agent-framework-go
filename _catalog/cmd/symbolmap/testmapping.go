// Copyright (c) Microsoft. All rights reserved.

package main

import "github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"

type testRef struct {
	assembly string
	owner    string
	method   string
}

// flattenTests reads only actual pairs from a validated catalog; null leaves
// remain in the declaration projection.
func flattenTests(c symbolcatalog.Catalog) map[testRef]string {
	pairs := make(map[testRef]string)
	for assembly, types := range c.Tests {
		for owner, methods := range types {
			for method, target := range methods {
				if target != nil {
					pairs[testRef{assembly: assembly, owner: owner, method: method}] = *target
				}
			}
		}
	}
	return pairs
}
