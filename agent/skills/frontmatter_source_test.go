// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/skills"
)

func mustInlineSkill(frontmatter skills.Frontmatter, content string, resources []skills.Resource, scripts []skills.Script) *skills.Skill {
	if err := frontmatter.Validate(); err != nil {
		panic(err)
	}
	return &skills.Skill{
		Frontmatter: frontmatter,
		GetContent: func(context.Context) (string, error) {
			return content, nil
		},
		Resources: append([]skills.Resource(nil), resources...),
		Scripts:   append([]skills.Script(nil), scripts...),
	}
}

func TestValidateName_ValidName(t *testing.T) {
	valid := []string{"my-skill", "a", "skill123", "a1b2c3"}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if err := (skills.Frontmatter{Name: name, Description: "A valid description."}).Validate(); err != nil {
				t.Fatalf("expected %q to be valid: %v", name, err)
			}
		})
	}
}

func TestValidateName_InvalidName(t *testing.T) {
	invalid := []string{"-leading-hyphen", "trailing-hyphen-", "has spaces", "UPPERCASE", "consecutive--hyphens", "special!chars"}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			err := (skills.Frontmatter{Name: name, Description: "A valid description."}).Validate()
			if err == nil {
				t.Fatalf("expected %q to be invalid", name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "skill name") {
				t.Fatalf("expected name validation error, got %v", err)
			}
		})
	}
}

func TestValidateName_NameExceedsMaxLength(t *testing.T) {
	if err := (skills.Frontmatter{Name: strings.Repeat("a", 65), Description: "A valid description."}).Validate(); err == nil {
		t.Fatal("expected long name to fail validation")
	}
}

func TestValidateName_Whitespace_ReturnsError(t *testing.T) {
	for _, name := range []string{"", "   "} {
		t.Run(name, func(t *testing.T) {
			if err := (skills.Frontmatter{Name: name, Description: "A valid description."}).Validate(); err == nil {
				t.Fatal("expected empty or whitespace name to fail validation")
			}
		})
	}
}

func TestValidateDescription_ValidDescription(t *testing.T) {
	if err := (skills.Frontmatter{Name: "my-skill", Description: "A valid description."}).Validate(); err != nil {
		t.Fatalf("expected description to be valid: %v", err)
	}
}

func TestValidateDescription_DescriptionExceedsMaxLength(t *testing.T) {
	if err := (skills.Frontmatter{Name: "my-skill", Description: strings.Repeat("x", 1025)}).Validate(); err == nil {
		t.Fatal("expected long description to fail validation")
	}
}

func TestValidateDescription_Whitespace_ReturnsError(t *testing.T) {
	for _, description := range []string{"", "   "} {
		t.Run(description, func(t *testing.T) {
			if err := (skills.Frontmatter{Name: "my-skill", Description: description}).Validate(); err == nil {
				t.Fatal("expected empty or whitespace description to fail validation")
			}
		})
	}
}

func TestValidateCompatibility_WithinMaxLength_ReturnsNil(t *testing.T) {
	if err := (skills.Frontmatter{Name: "my-skill", Description: "A valid description.", Compatibility: strings.Repeat("x", 500)}).Validate(); err != nil {
		t.Fatalf("expected compatibility to be valid: %v", err)
	}
}

func TestValidateCompatibility_ExceedsMaxLength_ReturnsError(t *testing.T) {
	if err := (skills.Frontmatter{Name: "my-skill", Description: "A valid description.", Compatibility: strings.Repeat("x", 501)}).Validate(); err == nil {
		t.Fatal("expected long compatibility to fail validation")
	}
}

func TestInMemorySource_ValidSkills_ReturnsAll(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "my-skill", Description: "A valid skill."}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "another", Description: "Another valid skill."}, "More instructions.", nil, nil),
	)

	result, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result))
	}
	if result[0].Frontmatter.Name != "my-skill" || result[1].Frontmatter.Name != "another" {
		t.Fatalf("unexpected skills returned: %q, %q", result[0].Frontmatter.Name, result[1].Frontmatter.Name)
	}
}
