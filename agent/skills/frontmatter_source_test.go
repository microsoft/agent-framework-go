// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"slices"
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
		Resources: slices.Clone(resources),
		Scripts:   slices.Clone(scripts),
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

func TestValidateCompatibility_Unset_ReturnsNil(t *testing.T) {
	if err := (skills.Frontmatter{Name: "my-skill", Description: "A valid description."}).Validate(); err != nil {
		t.Fatalf("unset compatibility must be valid: %v", err)
	}
}

func TestFrontmatter_UnicodeLengthLimits(t *testing.T) {
	for _, tc := range []struct {
		name          string
		description   string
		compatibility string
		valid         bool
	}{
		{"BMP description at limit", strings.Repeat("\u00e9", 1024), "", true},
		{"BMP description over limit", strings.Repeat("\u00e9", 1025), "", false},
		{"supplementary description at limit", strings.Repeat("\U0001f600", 512), "", true},
		{"supplementary description over limit", strings.Repeat("\U0001f600", 513), "", false},
		{"mixed description at limit", strings.Repeat("x", 1023) + "\u00e9", "", true},
		{"BMP compatibility at limit", "Description", strings.Repeat("\u00e9", 500), true},
		{"BMP compatibility over limit", "Description", strings.Repeat("\u00e9", 501), false},
		{"supplementary compatibility at limit", "Description", strings.Repeat("\U0001f600", 250), true},
		{"supplementary compatibility over limit", "Description", strings.Repeat("\U0001f600", 251), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (skills.Frontmatter{Name: "unicode-skill", Description: tc.description, Compatibility: tc.compatibility}).Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, valid=%t", err, tc.valid)
			}
		})
	}
}

func TestFrontmatter_ConstructionRejectsInvalidName(t *testing.T) {
	for _, name := range []string{"UPPERCASE", "-leading", "trailing-", "consecutive--hyphens"} {
		t.Run(name, func(t *testing.T) {
			frontmatter := skills.Frontmatter{Name: name, Description: "A valid description."}
			if err := frontmatter.Validate(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "name") {
				t.Fatalf("invalid frontmatter name error = %v", err)
			}
		})
	}
}

func TestFrontmatter_ConstructionRejectsLongName(t *testing.T) {
	frontmatter := skills.Frontmatter{Name: strings.Repeat("a", 65), Description: "A valid description."}
	if err := frontmatter.Validate(); err == nil {
		t.Fatal("construction accepted a name longer than 64 characters")
	}
}

func TestFrontmatter_ConstructionRejectsLongDescription(t *testing.T) {
	frontmatter := skills.Frontmatter{Name: "valid-name", Description: strings.Repeat("x", 1025)}
	if err := frontmatter.Validate(); err == nil {
		t.Fatal("construction accepted a description longer than 1024 characters")
	}
}

func TestFrontmatter_ConstructionRejectsBlankName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		t.Run(name, func(t *testing.T) {
			frontmatter := skills.Frontmatter{Name: name, Description: "A valid description."}
			if err := frontmatter.Validate(); err == nil {
				t.Fatal("construction accepted an empty or whitespace name")
			}
		})
	}
}

func TestFrontmatter_ConstructionRejectsBlankDescription(t *testing.T) {
	for _, description := range []string{"", "   "} {
		t.Run(description, func(t *testing.T) {
			frontmatter := skills.Frontmatter{Name: "valid-name", Description: description}
			if err := frontmatter.Validate(); err == nil {
				t.Fatal("construction accepted an empty or whitespace description")
			}
		})
	}
}

func TestFrontmatter_CompatibilityAssignmentRejectsLongValue(t *testing.T) {
	frontmatter := skills.Frontmatter{Name: "valid-name", Description: "A valid description."}
	frontmatter.Compatibility = strings.Repeat("x", 501)
	if err := frontmatter.Validate(); err == nil {
		t.Fatal("validation accepted compatibility longer than 500 characters")
	}
}

func TestFrontmatter_CompatibilityAssignmentAcceptsMaxLength(t *testing.T) {
	frontmatter := skills.Frontmatter{Name: "valid-name", Description: "A valid description."}
	compatibility := strings.Repeat("x", 500)
	frontmatter.Compatibility = compatibility
	if err := frontmatter.Validate(); err != nil {
		t.Fatal(err)
	}
	if frontmatter.Compatibility != compatibility {
		t.Fatalf("compatibility = %q, want assigned value", frontmatter.Compatibility)
	}
}

func TestFrontmatter_CompatibilityAssignmentAcceptsAbsentValue(t *testing.T) {
	frontmatter := skills.Frontmatter{Name: "valid-name", Description: "A valid description."}
	frontmatter.Compatibility = ""
	if err := frontmatter.Validate(); err != nil {
		t.Fatal(err)
	}
	if frontmatter.Compatibility != "" {
		t.Fatalf("compatibility = %q, want absent value", frontmatter.Compatibility)
	}
}

func TestFrontmatter_ConstructionSetsCompatibility(t *testing.T) {
	frontmatter := skills.Frontmatter{
		Name: "valid-name", Description: "A valid description.", Compatibility: "Requires Python 3.10+",
	}
	if err := frontmatter.Validate(); err != nil {
		t.Fatal(err)
	}
	if frontmatter.Compatibility != "Requires Python 3.10+" {
		t.Fatalf("compatibility = %q", frontmatter.Compatibility)
	}
}

func TestFrontmatter_ConstructionRejectsLongCompatibility(t *testing.T) {
	frontmatter := skills.Frontmatter{
		Name: "valid-name", Description: "A valid description.", Compatibility: strings.Repeat("x", 501),
	}
	if err := frontmatter.Validate(); err == nil {
		t.Fatal("construction accepted compatibility longer than 500 characters")
	}
}

func TestFrontmatter_InvalidUppercaseNameIsRejected(t *testing.T) {
	frontmatter := skills.Frontmatter{Name: "INVALID-NAME", Description: "An invalid skill."}
	if err := frontmatter.Validate(); err == nil {
		t.Fatal("invalid frontmatter was accepted")
	}
}

func TestInMemorySource_ValidSkills_ReturnsAll(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "my-skill", Description: "A valid skill."}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "another", Description: "Another valid skill."}, "More instructions.", nil, nil),
	)

	result, err := source.Skills(t.Context(), skills.SourceContext{})
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

func TestInMemorySource_CodeDefinedSkills_ReturnsAll(t *testing.T) {
	minimal := mustInlineSkill(skills.Frontmatter{Name: "minimal", Description: "A minimal skill."}, "Minimal skill body.", nil, nil)
	full := mustInlineSkill(
		skills.Frontmatter{Name: "full", Description: "A full skill with resources and scripts."},
		"Full skill body.",
		[]skills.Resource{{Name: "test-resource", Read: func(context.Context) (any, error) { return "resource content", nil }}},
		[]skills.Script{{Name: "TestScript", ParametersSchema: `{"type":"object","properties":{"value":{"type":"number"}}}`}},
	)
	source := skills.NewInMemorySource(minimal, full)

	result, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Frontmatter.Name != "minimal" || result[1].Frontmatter.Name != "full" {
		t.Fatalf("Skills() = %#v, want minimal and full in registration order", result)
	}
}
