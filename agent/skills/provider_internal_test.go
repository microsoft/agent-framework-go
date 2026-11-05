// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/microsoft/agent-framework-go/agent"
)

// Cache waiters retry the build path after an unsuccessful owner; that retry
// must preserve the same panic-to-error behavior as the cache owner.
func TestProvider_CacheWaiterBuildPanicIsReturnedAsError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		provider := NewContextProvider(ContextProviderOptions{
			Sources: []Source{SourceFunc(func(context.Context, SourceContext) ([]*Skill, error) {
				started <- struct{}{}
				<-release
				panic("boom")
			})},
		})
		done := make(chan error, 2)
		invoke := func() {
			_, _, err := provider.Invoking(t.Context(), agent.InvokingContext{})
			done <- err
		}
		go invoke()
		<-started
		go invoke()
		synctest.Wait()
		close(release)
		for range 2 {
			if err := <-done; err == nil || !strings.Contains(err.Error(), "building skills context panicked: boom") {
				t.Fatalf("provider error = %v, want recovered source panic", err)
			}
		}
	})
}

func fullLookupSkill() *Skill {
	return &Skill{
		Frontmatter: Frontmatter{Name: "full", Description: "A full skill with resources and scripts."},
		GetContent:  func(context.Context) (string, error) { return "Full skill body.", nil },
		Resources: []Resource{{
			Name: "test-resource",
			Read: func(context.Context) (any, error) { return "resource content", nil },
		}},
		Scripts: []Script{{Name: "TestScript", ParametersSchema: `{"type":"object","properties":{"value":{"type":"number"}}}`}},
	}
}

func TestProvidedCodeDefinedSkill_ExistingResource_ReturnsDescriptor(t *testing.T) {
	skill := newProvidedSkill(fullLookupSkill())

	resource, ok := skill.lookupResource("test-resource")
	if !ok || resource.Name != "test-resource" {
		t.Fatalf("lookupResource() = %#v, %t, want test-resource", resource, ok)
	}
}

func TestProvidedCodeDefinedSkill_MissingResource_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(fullLookupSkill())

	if resource, ok := skill.lookupResource("missing"); ok {
		t.Fatalf("lookupResource() = %#v, want not found", resource)
	}
}

func TestProvidedCodeDefinedSkill_NoResources_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "minimal", Description: "A minimal skill."},
		GetContent:  func(context.Context) (string, error) { return "Minimal skill body.", nil },
	})

	if resource, ok := skill.lookupResource("anything"); ok {
		t.Fatalf("lookupResource() = %#v, want not found", resource)
	}
}

func TestProvidedCodeDefinedSkill_ExistingScript_ReturnsDescriptor(t *testing.T) {
	skill := newProvidedSkill(fullLookupSkill())

	script, ok := skill.lookupScript("TestScript")
	if !ok || script.Name != "TestScript" {
		t.Fatalf("lookupScript() = %#v, %t, want TestScript", script, ok)
	}
}

func TestProvidedCodeDefinedSkill_MissingScript_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(fullLookupSkill())

	if script, ok := skill.lookupScript("missing"); ok {
		t.Fatalf("lookupScript() = %#v, want not found", script)
	}
}

func TestProvidedCodeDefinedSkill_NoScripts_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "minimal", Description: "A minimal skill."},
		GetContent:  func(context.Context) (string, error) { return "Minimal skill body.", nil },
	})

	if script, ok := skill.lookupScript("anything"); ok {
		t.Fatalf("lookupScript() = %#v, want not found", script)
	}
}

func TestInlineSkill_WithoutAddedScripts_HasNilCollection(t *testing.T) {
	skill := &Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
	}

	if scripts := skill.Scripts; scripts != nil {
		t.Fatalf("Scripts = %#v, want nil when no scripts were added", scripts)
	}
}

func TestProvidedInlineSkill_WithoutAddedScripts_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
	})

	if script, ok := skill.lookupScript("nonexistent"); ok {
		t.Fatalf("lookupScript() = %#v, want not found", script)
	}
}

func TestProvidedInlineSkill_ExistingResource_ReturnsDescriptor(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
		Resources: []Resource{
			{Name: "r1", Read: func(context.Context) (any, error) { return "v1", nil }},
			{Name: "r2", Read: func(context.Context) (any, error) { return "v2", nil }},
		},
	})

	resource, ok := skill.lookupResource("r2")
	if !ok || resource.Name != "r2" {
		t.Fatalf("lookupResource() = %#v, %t, want r2", resource, ok)
	}
}

func TestProvidedInlineSkill_MissingResource_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
		Resources:   []Resource{{Name: "r1", Read: func(context.Context) (any, error) { return "v1", nil }}},
	})

	if resource, ok := skill.lookupResource("missing"); ok {
		t.Fatalf("lookupResource() = %#v, want not found", resource)
	}
}

func TestProvidedInlineSkill_NoResources_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
	})

	if resource, ok := skill.lookupResource("missing"); ok {
		t.Fatalf("lookupResource() = %#v, want not found", resource)
	}
}

func TestProvidedInlineSkill_ExistingScript_ReturnsDescriptor(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
		Scripts: []Script{
			{Name: "s1", Run: func(context.Context, *Skill, json.RawMessage) (any, error) { return "first", nil }},
			{Name: "s2", Run: func(context.Context, *Skill, json.RawMessage) (any, error) { return "second", nil }},
		},
	})

	script, ok := skill.lookupScript("s2")
	if !ok || script.Name != "s2" {
		t.Fatalf("lookupScript() = %#v, %t, want s2", script, ok)
	}
}

func TestProvidedInlineSkill_MissingScript_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
		Scripts:     []Script{{Name: "s1", Run: func(context.Context, *Skill, json.RawMessage) (any, error) { return "ok", nil }}},
	})

	if script, ok := skill.lookupScript("missing"); ok {
		t.Fatalf("lookupScript() = %#v, want not found", script)
	}
}

func TestProvidedInlineSkill_NoScripts_ReturnsNotFound(t *testing.T) {
	skill := newProvidedSkill(&Skill{
		Frontmatter: Frontmatter{Name: "my-skill", Description: "A valid skill."},
		GetContent:  func(context.Context) (string, error) { return "Instructions.", nil },
	})

	if script, ok := skill.lookupScript("missing"); ok {
		t.Fatalf("lookupScript() = %#v, want not found", script)
	}
}
