// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"fmt"
	"strings"
)

const (
	// maxNameLength is the maximum allowed skill name length.
	maxNameLength = 64
	// maxDescriptionLength is the maximum allowed skill description length.
	maxDescriptionLength = 1024
	// maxCompatibilityLength is the maximum allowed compatibility length.
	maxCompatibilityLength = 500
)

// Frontmatter represents the parsed YAML frontmatter metadata from a SKILL.md file.
type Frontmatter struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	AllowedTools  string
	Metadata      map[string]any
}

// Validate validates the frontmatter according to the Agent Skills specification.
func (f Frontmatter) Validate() error {
	if err := validateName(f.Name); err != nil {
		return err
	}
	if err := validateDescription(f.Description); err != nil {
		return err
	}
	if err := validateCompatibility(f.Compatibility); err != nil {
		return err
	}
	return nil
}

// Source provides skills from a specific origin.
type Source interface {
	Skills(context.Context) ([]*Skill, error)
}

// SourceFunc wraps a function to be used as a Source.
type SourceFunc func(context.Context) ([]*Skill, error)

func (f SourceFunc) Skills(ctx context.Context) ([]*Skill, error) {
	return f(ctx)
}

// Skill describes a domain-specific capability with instructions, resources, and scripts.
type Skill struct {
	Frontmatter Frontmatter
	// GetContent lazily loads the skill's instruction text. For file-based skills
	// this is the raw SKILL.md file content. For code-defined skills this may be
	// a synthesized document containing name, description, and body.
	GetContent           func(context.Context) (string, error)
	Resources            []Resource
	Scripts              []Script
	AdditionalProperties map[string]any
}

// Resource is supplementary skill content that can be read on demand.
type Resource struct {
	Name                 string
	Description          string
	Read                 func(context.Context) (any, error)
	AdditionalProperties map[string]any
}

// Script is executable skill functionality that can be run on demand.
//
// Arguments passed to [Script.Run] are positional CLI-style string tokens, for
// example ["--value", "26.2", "--factor", "1.60934"]. The LLM is instructed to
// pass a JSON array of strings, and the run_skill_script tool forwards those
// strings verbatim. Code-defined scripts may parse the strings however they
// choose; file-based scripts pass them directly to the subprocess.
type Script struct {
	Name                 string
	Description          string
	Run                  func(context.Context, *Skill, []string) (any, error)
	AdditionalProperties map[string]any
}

// ScriptRunner defines the function signature for running a script.
//
// The args slice contains positional CLI-style string tokens as sent by the LLM
// (for example ["--value", "26.2", "--factor", "1.60934"]).
type ScriptRunner func(context.Context, *Skill, *Script, []string) (any, error)

// validateName validates a skill name.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("skill name is required")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("skill name must be %d characters or fewer", maxNameLength)
	}
	if !isValidSkillName(name) {
		return fmt.Errorf("skill name must use only lowercase letters, numbers, and single hyphens, and must not start or end with a hyphen or contain consecutive hyphens")
	}
	return nil
}

func isValidSkillName(name string) bool {
	previousHyphen := false
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			previousHyphen = false
		case r >= '0' && r <= '9':
			previousHyphen = false
		case r == '-':
			if i == 0 || previousHyphen {
				return false
			}
			previousHyphen = true
		default:
			return false
		}
	}

	return !previousHyphen
}

// validateDescription validates a skill description.
func validateDescription(description string) error {
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("skill description is required")
	}
	if len(description) > maxDescriptionLength {
		return fmt.Errorf("skill description must be %d characters or fewer", maxDescriptionLength)
	}
	return nil
}

// validateCompatibility validates an optional compatibility value.
func validateCompatibility(compatibility string) error {
	if len(compatibility) > maxCompatibilityLength {
		return fmt.Errorf("skill compatibility must be %d characters or fewer", maxCompatibilityLength)
	}
	return nil
}
