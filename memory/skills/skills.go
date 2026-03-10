// Copyright (c) Microsoft. All rights reserved.

// Package skills provides an implementation of the Agent Skills specification
// (https://agentskills.io/) for discovering and exposing skills from filesystem directories
// via a memory.ContextProvider.
package skills

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const defaultSkillsInstructionPrompt = `You have access to skills containing domain-specific knowledge and capabilities.
Each skill provides specialized instructions, reference documents, and assets for specific tasks.

<available_skills>
%s
</available_skills>

When a task aligns with a skill's domain:
1. Use ` + "`load_skill`" + ` to retrieve the skill's instructions
2. Follow the provided guidance
3. Use ` + "`read_skill_resource`" + ` to read any references or other files mentioned by the skill

Only load what is needed, when it is needed.`

// Config configures the skills context provider.
type Config struct {
	// SourceID is the context provider SourceID. Defaults to "skills".
	SourceID string

	// SkillsInstructionPrompt is a custom system prompt template for advertising skills.
	// Use %s as the placeholder for the generated skills list.
	// When empty, a default template is used.
	SkillsInstructionPrompt string

	// Logger is an optional structured logger for skill loading diagnostics.
	Logger *slog.Logger
}

type provider struct {
	skills                  map[string]*fileAgentSkill
	loader                  *skillLoader
	tools                   []tool.Tool
	skillsInstructionPrompt string
}

// New creates a memory.ContextProvider that searches the given filesystems for skills.
//
// Each fs.FS can represent an individual skill folder (containing a SKILL.md file) or a parent folder
// with skill subdirectories. The provider discovers SKILL.md files up to 2 levels deep.
//
// Use os.DirFS to create an fs.FS from a directory path on disk.
func New(opts *Config, fsys ...fs.FS) *memory.ContextProvider {
	if opts == nil {
		opts = &Config{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	loader := &skillLoader{logger: logger}
	loaded := loader.discoverAndLoadSkills(fsys)
	instructionPrompt := buildSkillsInstructionPrompt(opts, loaded)

	p := &provider{
		skills:                  loaded,
		loader:                  loader,
		skillsInstructionPrompt: instructionPrompt,
	}

	p.tools = []tool.Tool{
		functool.MustNew(
			&functool.Func{
				Name:        "load_skill",
				Description: "Loads the full instructions for a specific skill.",
			},
			func(_ tool.Context, in struct {
				SkillName string `json:"skillName" jsonschema:"The name of the skill to load"`
			}) (string, error) {
				return p.loadSkill(in.SkillName)
			},
		),
		functool.MustNew(
			&functool.Func{
				Name:        "read_skill_resource",
				Description: "Reads a file associated with a skill, such as references or assets.",
			},
			func(_ tool.Context, in struct {
				SkillName    string `json:"skillName" jsonschema:"The name of the skill"`
				ResourceName string `json:"resourceName" jsonschema:"The relative path of the resource file within the skill"`
			}) (string, error) {
				return p.readSkillResource(in.SkillName, in.ResourceName)
			},
		),
	}

	sourceID := opts.SourceID
	if sourceID == "" {
		sourceID = "skills"
	}

	return &memory.ContextProvider{
		SourceID: sourceID,
		Provide: func(_ memory.BeforeRunContext) (memory.Context, error) {
			if len(p.skills) == 0 {
				return memory.Context{}, nil
			}
			out := memory.Context{Tools: p.tools}
			if p.skillsInstructionPrompt != "" {
				out.Messages = []*message.Message{{
					Role: message.RoleSystem,
					Contents: []message.Content{
						&message.TextContent{Text: p.skillsInstructionPrompt},
					},
				}}
			}
			return out, nil
		},
	}
}

func (p *provider) loadSkill(skillName string) (string, error) {
	if strings.TrimSpace(skillName) == "" {
		return "Error: Skill name cannot be empty.", nil
	}
	skill, ok := p.skills[strings.ToLower(skillName)]
	if !ok {
		return fmt.Sprintf("Error: Skill '%s' not found.", skillName), nil
	}
	p.loader.logger.Info("Loading skill", "skillName", skillName)
	return skill.body, nil
}

func (p *provider) readSkillResource(skillName, resourceName string) (string, error) {
	if strings.TrimSpace(skillName) == "" {
		return "Error: Skill name cannot be empty.", nil
	}
	if strings.TrimSpace(resourceName) == "" {
		return "Error: Resource name cannot be empty.", nil
	}
	skill, ok := p.skills[strings.ToLower(skillName)]
	if !ok {
		return fmt.Sprintf("Error: Skill '%s' not found.", skillName), nil
	}
	content, err := p.loader.readSkillResource(skill, resourceName)
	if err != nil {
		p.loader.logger.Error("Failed to read resource from skill",
			"resourceName", resourceName,
			"skillName", skillName,
			"error", err)
		return fmt.Sprintf("Error: Failed to read resource '%s' from skill '%s'.", resourceName, skillName), nil
	}
	return content, nil
}

func buildSkillsInstructionPrompt(opts *Config, skills map[string]*fileAgentSkill) string {
	if len(skills) == 0 {
		return ""
	}

	promptTemplate := defaultSkillsInstructionPrompt
	if opts != nil && opts.SkillsInstructionPrompt != "" {
		promptTemplate = opts.SkillsInstructionPrompt
	}

	// Order by name for deterministic prompt output.
	sorted := make([]*fileAgentSkill, 0, len(skills))
	for _, skill := range skills {
		sorted = append(sorted, skill)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].frontmatter.Name < sorted[j].frontmatter.Name
	})

	var sb strings.Builder
	for _, skill := range sorted {
		sb.WriteString("  <skill>\n")
		sb.WriteString(fmt.Sprintf("    <name>%s</name>\n", xmlEscape(skill.frontmatter.Name)))
		sb.WriteString(fmt.Sprintf("    <description>%s</description>\n", xmlEscape(skill.frontmatter.Description)))
		sb.WriteString("  </skill>\n")
	}

	return fmt.Sprintf(promptTemplate, strings.TrimRight(sb.String(), "\n"))
}

// xmlEscape escapes XML-sensitive characters for safe prompt embedding.
func xmlEscape(s string) string {
	var sb strings.Builder
	if err := xml.EscapeText(&sb, []byte(s)); err != nil {
		return s
	}
	return sb.String()
}

// frontmatter represents the parsed YAML frontmatter from a SKILL.md file,
// containing the skill's name and description.
type frontmatter struct {
	Name        string
	Description string
}

// fileAgentSkill represents a loaded Agent Skill discovered from a filesystem directory.
//
// Each skill is backed by a SKILL.md file containing YAML frontmatter (name and description)
// and a markdown body with instructions. Resource files referenced in the body are validated at
// discovery time and read from disk on demand.
type fileAgentSkill struct {
	// frontmatter is the parsed YAML frontmatter (name and description).
	frontmatter frontmatter
	// body is the SKILL.md body content (without the YAML frontmatter).
	body string
	// fs is the sub-filesystem rooted at the skill directory.
	fs fs.FS
	// sourcePath is the relative path within the parent FS, used for logging.
	sourcePath string
	// resourceNames is the relative paths of resource files referenced in the skill body.
	resourceNames []string
}
