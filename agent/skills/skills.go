// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/microsoft/agent-framework-go/agent"
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
// Description and compatibility limits count UTF-16 code units.
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
//
// The source context identifies the initiating agent and invocation session.
// Sources that vary by session must disable caching or use an appropriate
// [ContextProviderOptions.CacheIsolationKeySelector].
// The provider borrows sources unless [ContextProviderOptions.OwnsSources] is set.
// Sources return valid, non-nil skills. Returned slices and skills must be
// treated as read-only unless the source explicitly transfers ownership.
type Source interface {
	Skills(context.Context, SourceContext) ([]*Skill, error)
}

// SourceContext identifies the agent and session requesting skills. Session is
// nil when the invocation has no session. Agent is set during [agent.Agent.Run]
// and may be nil when a provider or source is called directly.
// Treat the context as read-only; session state remains application-managed.
type SourceContext struct {
	Agent   *agent.Agent
	Session *agent.Session
}

// SourceFunc wraps a function to be used as a Source.
type SourceFunc func(context.Context, SourceContext) ([]*Skill, error)

func (f SourceFunc) Skills(ctx context.Context, sourceContext SourceContext) ([]*Skill, error) {
	if f == nil {
		return nil, fmt.Errorf("skills: source function is nil")
	}
	return f(ctx, sourceContext)
}

// Skill describes a domain-specific capability with instructions, resources, and scripts.
type Skill struct {
	Frontmatter Frontmatter
	// GetContent lazily loads the skill's instruction text. For file-based skills
	// this is the raw SKILL.md file content. For code-defined skills this may be
	// a synthesized document containing name, description, and body.
	// Errors, including cancellation, propagate to the load_skill caller.
	GetContent           func(context.Context) (string, error)
	Resources            []Resource
	Scripts              []Script
	AdditionalProperties map[string]any
}

// Resource is supplementary skill content that can be read on demand.
type Resource struct {
	Name        string
	Description string
	// Read loads the resource. Its error is propagated to the tool caller,
	// allowing invocation middleware to apply its error-detail policy.
	Read                 func(context.Context) (any, error)
	AdditionalProperties map[string]any
}

// Script is executable skill functionality that can be run on demand.
//
// Arguments passed to [Script.Run] are JSON values. Scripts decode and validate
// their own input format, such as a named JSON object or a positional string
// array. Missing arguments are nil; an explicit JSON null is preserved.
type Script struct {
	Name        string
	Description string
	// ParametersSchema is an optional JSON schema string describing the argument
	// format expected by the script. When set, file-based skills include the schema
	// in the skill's <available_scripts> block so the LLM knows how to format
	// arguments. Empty means no schema.
	ParametersSchema     string
	Run                  func(context.Context, *Skill, json.RawMessage) (any, error)
	AdditionalProperties map[string]any
}

// ScriptRunner runs a file-backed script with JSON arguments. It receives
// the owning skill and script metadata, including the backing filesystem.
// The runner is responsible for decoding and validating its argument format.
type ScriptRunner func(context.Context, *Skill, *Script, json.RawMessage) (any, error)

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
	if utf16Length(description) > maxDescriptionLength {
		return fmt.Errorf("skill description must be %d characters or fewer", maxDescriptionLength)
	}
	return nil
}

// validateCompatibility validates an optional compatibility value.
func validateCompatibility(compatibility string) error {
	if utf16Length(compatibility) > maxCompatibilityLength {
		return fmt.Errorf("skill compatibility must be %d characters or fewer", maxCompatibilityLength)
	}
	return nil
}

func utf16Length(value string) int {
	length := 0
	for _, r := range value {
		length += utf16.RuneLen(r)
	}
	return length
}
