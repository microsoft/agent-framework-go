// Copyright (c) Microsoft. All rights reserved.

// Package skills provides an implementation of the Agent Skills specification
// (https://agentskills.io/) for discovering and exposing skills from filesystem directories.
//
// Skills follow the progressive disclosure pattern:
//  1. Advertise — skill names and descriptions are injected into the system prompt.
//  2. Load — the full SKILL.md body is returned via the load_skill tool.
//  3. Read resources — supplementary files are read from disk on demand via the read_skill_resource tool.
//
// Skills are discovered by searching configured directories for SKILL.md files.
// Referenced resources are validated at initialization; invalid skills are excluded and logged.
//
// Security: this provider only reads static content. Skill metadata is XML-escaped
// before prompt embedding, and resource reads are guarded against path traversal and symlink escape.
// Only use skills from trusted sources.
package skills

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"sort"
	"strings"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const maximumAutocallIterations = 40

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

// Config configures the middleware.
type Config struct {
	// SkillsInstructionPrompt is a custom system prompt template for advertising skills.
	// Use %s as the placeholder for the generated skills list.
	// When empty, a default template is used.
	SkillsInstructionPrompt string

	// Logger is an optional structured logger for skill loading diagnostics.
	Logger *slog.Logger
}

// skills is a [middleware.Middleware] that discovers and exposes Agent Skills
// from filesystem directories.
//
// It implements the progressive disclosure pattern from the Agent Skills specification
// (https://agentskills.io/):
//  1. Advertise — skill names and descriptions are injected into the system prompt (~100 tokens per skill).
//  2. Load — the full SKILL.md body is returned via the load_skill tool.
//  3. Read resources — supplementary files are read from disk on demand via the read_skill_resource tool.
type skills struct {
	skills                  map[string]*fileAgentSkill
	loader                  *skillLoader
	tools                   []*functool.Tool
	skillsInstructionPrompt string
}

// New creates a new [middleware.Middleware] that searches the given filesystems for skills.
//
// Each fs.FS can represent an individual skill folder (containing a SKILL.md file) or a parent folder
// with skill subdirectories. The provider discovers SKILL.md files up to 2 levels deep.
//
// Use [os.DirFS] to create an fs.FS from a directory path on disk.
func New(opts *Config, fsys ...fs.FS) middleware.Middleware {
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

	p := &skills{
		skills:                  loaded,
		loader:                  loader,
		skillsInstructionPrompt: instructionPrompt,
	}

	p.tools = []*functool.Tool{
		functool.MustNew(
			&functool.Func{
				Name:        "load_skill",
				Description: "Loads the full instructions for a specific skill.",
			},
			func(_ context.Context, in struct {
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
			func(_ context.Context, in struct {
				SkillName    string `json:"skillName" jsonschema:"The name of the skill"`
				ResourceName string `json:"resourceName" jsonschema:"The relative path of the resource file within the skill"`
			}) (string, error) {
				return p.readSkillResource(in.SkillName, in.ResourceName)
			},
		),
	}

	return p
}

// Run implements [middleware.Middleware].
func (p *skills) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	runMessages := messages
	runOpts := opts
	if len(p.skills) != 0 {
		if p.skillsInstructionPrompt != "" {
			runMessages = append([]*message.Message{{
				Role: message.RoleSystem,
				Contents: []message.Content{
					&message.TextContent{Text: p.skillsInstructionPrompt},
				},
			}}, runMessages...)
		}
		for _, tl := range p.tools {
			runOpts = append(runOpts, agentopt.Tool(tl))
		}
	}

	return func(yield func(*message.ResponseUpdate, error) bool) {
		currentMessages := append([]*message.Message(nil), runMessages...)
		for range maximumAutocallIterations {
			var resp message.Response
			for update, err := range next(ctx, currentMessages, runOpts...) {
				if err != nil {
					yield(nil, err)
					return
				}
				if update != nil {
					resp.Update(update)
				}
				if !yield(update, nil) {
					return
				}
			}
			resp.Coalesce()

			toolMsg := p.autocallToolMessage(ctx, resp.Messages)
			if toolMsg == nil {
				return
			}
			if !yield(&message.ResponseUpdate{Role: toolMsg.Role, Contents: toolMsg.Contents}, nil) {
				return
			}

			currentMessages = append(currentMessages, resp.Messages...)
			currentMessages = append(currentMessages, toolMsg)
		}

		p.loader.logger.Warn("Reached maximum skill autocall iterations", "maximumIterations", maximumAutocallIterations)
	}
}

func (p *skills) autocallToolMessage(ctx context.Context, responseMessages []*message.Message) *message.Message {
	if len(responseMessages) == 0 {
		return nil
	}

	existingResults := make(map[string]struct{})
	for _, msg := range responseMessages {
		for _, content := range msg.Contents {
			if frc, ok := content.(*message.FunctionResultContent); ok {
				existingResults[frc.CallID] = struct{}{}
			}
		}
	}

	results := make([]message.Content, 0)
	for _, msg := range responseMessages {
		for _, content := range msg.Contents {
			fcc, ok := content.(*message.FunctionCallContent)
			if !ok {
				continue
			}
			if _, exists := existingResults[fcc.CallID]; exists {
				continue
			}

			result, handled := p.autocallSkillTool(ctx, fcc)
			if !handled {
				continue
			}

			results = append(results, &message.FunctionResultContent{
				CallID: fcc.CallID,
				Result: result,
			})
			existingResults[fcc.CallID] = struct{}{}
		}
	}

	if len(results) == 0 {
		return nil
	}

	return &message.Message{
		Role:     message.RoleTool,
		Contents: results,
	}
}

func (p *skills) autocallSkillTool(ctx context.Context, fcc *message.FunctionCallContent) (string, bool) {
	for _, tl := range p.tools {
		if tl.Name() != fcc.Name {
			continue
		}
		result, err := tl.Call(ctx, fcc.Arguments)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		return result.(string), true
	}

	return "", false
}

func (p *skills) loadSkill(skillName string) (string, error) {
	if strings.TrimSpace(skillName) == "" {
		return "", fmt.Errorf("Error: Skill name cannot be empty.")
	}
	skill, ok := p.skills[strings.ToLower(skillName)]
	if !ok {
		return "", fmt.Errorf("Error: Skill '%s' not found.", skillName)
	}
	p.loader.logger.Info("Loading skill", "skillName", skillName)
	return skill.body, nil
}

func (p *skills) readSkillResource(skillName, resourceName string) (string, error) {
	if strings.TrimSpace(skillName) == "" {
		return "", fmt.Errorf("Error: Skill name cannot be empty.")
	}
	if strings.TrimSpace(resourceName) == "" {
		return "", fmt.Errorf("Error: Resource name cannot be empty.")
	}
	skill, ok := p.skills[strings.ToLower(skillName)]
	if !ok {
		return "", fmt.Errorf("Error: Skill '%s' not found.", skillName)
	}
	content, err := p.loader.readSkillResource(skill, resourceName)
	if err != nil {
		p.loader.logger.Error("Failed to read resource from skill",
			"resourceName", resourceName,
			"skillName", skillName,
			"error", err)
		return "", fmt.Errorf("Error: Failed to read resource '%s' from skill '%s'.", resourceName, skillName)
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
