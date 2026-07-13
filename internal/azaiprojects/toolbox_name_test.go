// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See License.txt in the project root for license information.

package azaiprojects

import (
	"context"
	"errors"
	"testing"
)

func TestValidateToolboxNamePathSegment(t *testing.T) {
	t.Parallel()

	validNames := []string{
		"toolbox",
		"tool.box",
		"toolbox:v1@prod",
		"toolbox(name)",
	}
	invalidNames := []string{
		".",
		"..",
		"toolbox/sub",
		"toolbox%2Fsub",
		"toolbox%252Fsub",
		"toolbox?query",
		"toolbox%3Fquery",
		"toolbox#fragment",
		"toolbox%23fragment",
		"toolbox\\sub",
		"toolbox%5Csub",
	}

	endpoints := []string{
		"https://example.test/base",
		"",
		"not-a-valid-url",
	}

	for _, endpoint := range endpoints {
		endpoint := endpoint
		t.Run("endpoint="+endpoint, func(t *testing.T) {
			t.Parallel()

			for _, name := range validNames {
				name := name
				t.Run("valid/"+name, func(t *testing.T) {
					t.Parallel()

					if err := validateToolboxNamePathSegment(endpoint, name); err != nil {
						t.Fatalf("validateToolboxNamePathSegment(%q) error = %v", name, err)
					}
				})
			}

			for _, name := range invalidNames {
				name := name
				t.Run("invalid/"+name, func(t *testing.T) {
					t.Parallel()

					err := validateToolboxNamePathSegment(endpoint, name)
					if !errors.Is(err, errToolboxNameMustBeSinglePathSegment) {
						t.Fatalf("validateToolboxNamePathSegment(%q) error = %v, want %v", name, err, errToolboxNameMustBeSinglePathSegment)
					}
				})
			}
		})
	}
}

func TestToolboxesClientRequestBuildersRejectUnsafeNames(t *testing.T) {
	t.Parallel()

	client := &ToolboxesClient{endpoint: "https://example.test/base"}
	ctx := context.Background()
	builders := map[string]func(string) error{
		"createToolboxVersion": func(name string) error {
			_, err := client.createToolboxVersionCreateRequest(ctx, name, nil, nil)
			return err
		},
		"deleteToolbox": func(name string) error {
			_, err := client.deleteToolboxCreateRequest(ctx, name, nil)
			return err
		},
		"deleteToolboxVersion": func(name string) error {
			_, err := client.deleteToolboxVersionCreateRequest(ctx, name, "v1", nil)
			return err
		},
		"getToolbox": func(name string) error {
			_, err := client.getToolboxCreateRequest(ctx, name, nil)
			return err
		},
		"getToolboxVersion": func(name string) error {
			_, err := client.getToolboxVersionCreateRequest(ctx, name, "v1", nil)
			return err
		},
		"listToolboxVersions": func(name string) error {
			_, err := client.listToolboxVersionsCreateRequest(ctx, name, nil)
			return err
		},
		"updateToolbox": func(name string) error {
			_, err := client.updateToolboxCreateRequest(ctx, name, "v1", nil)
			return err
		},
	}

	for builderName, builder := range builders {
		t.Run(builderName, func(t *testing.T) {
			t.Parallel()

			err := builder("toolbox%2Fsub")
			if !errors.Is(err, errToolboxNameMustBeSinglePathSegment) {
				t.Fatalf("%s error = %v, want %v", builderName, err, errToolboxNameMustBeSinglePathSegment)
			}
		})
	}
}
