// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"cmp"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	skillsPlaceholder               = "{skills}"
	resourceInstructionsPlaceholder = "{resource_instructions}"
	scriptInstructionsPlaceholder   = "{script_instructions}"
)

const defaultSkillsInstructionPrompt = `You have access to skills containing domain-specific knowledge and capabilities.
Each skill provides specialized instructions, reference documents, and assets for specific tasks.

<available_skills>
{skills}
</available_skills>

When a task aligns with a skill's domain, follow these steps in exact order:
- Use ` + "`load_skill`" + ` to retrieve the skill's instructions.
- Follow the provided guidance.
{resource_instructions}
{script_instructions}
Only load what is needed, when it is needed.`

// ContextProviderOptions configures a skills-backed agent.ContextProvider.
type ContextProviderOptions struct {
	// SourceID is the identifier for the provider's source in the resulting context.
	// Defaults to "skills" if not provided.
	SourceID string

	// SkillFilter optionally filters skills loaded from inline skills and sources.
	// Returning true keeps a skill; returning false excludes it.
	SkillFilter func(*Skill) bool

	// Skills provides in-memory skills to register with the provider.
	Skills []*Skill

	// Sources provides external skill sources to register with the provider.
	Sources []Source

	// SkillsInstructionPrompt is a custom system prompt template.
	// When empty, a default template is used.
	//
	// The template must contain {skills}, {resource_instructions}, and
	// {script_instructions}.
	SkillsInstructionPrompt string

	// ScriptApproval marks the run_skill_script tool as requiring approval.
	ScriptApproval bool

	// DisableCaching rebuilds instructions and tools for every invocation.
	DisableCaching bool

	// DisableSourceDeduplication preserves duplicate skill names from the configured
	// skills and sources instead of removing later duplicates.
	DisableSourceDeduplication bool

	// Logger is an optional structured logger for provider diagnostics.
	Logger *slog.Logger
}

type providedSkill struct {
	skill     *Skill
	resources map[string]Resource
	scripts   map[string]Script
}

type providedSkillSet struct {
	byName map[string]providedSkill
}

type providerState struct {
	sources []Source
	options ContextProviderOptions
	logger  *slog.Logger

	mu      sync.Mutex
	cached  *providerContext
	loading chan struct{}
}

type providerContext struct {
	Messages     []*message.Message
	Options      []agent.Option
	Instructions string
}

// NewContextProvider creates a skills context provider from the configured in-memory skills and sources.
func NewContextProvider(opts ContextProviderOptions) *agent.ContextProvider {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.SkillsInstructionPrompt != "" {
		if err := validatePromptTemplate(opts.SkillsInstructionPrompt); err != nil {
			panic(err)
		}
	}

	state := &providerState{
		sources: newProviderSources(opts.Skills, opts.Sources),
		options: opts,
		logger:  opts.Logger,
	}

	return &agent.ContextProvider{
		SourceID: cmp.Or(opts.SourceID, "skills"),
		Provide:  state.provide,
	}
}

// NewInMemorySource creates a skills source backed by the provided in-memory skills.
func NewInMemorySource(skills ...*Skill) Source {
	return newSkillSliceSource(skills...)
}

func newProviderSources(skills []*Skill, sources []Source) []Source {
	combined := make([]Source, 0, len(sources)+1)
	if len(skills) > 0 {
		combined = append(combined, newSkillSliceSource(skills...))
	}
	for i, source := range sources {
		if source == nil {
			panic(fmt.Sprintf("source %d is nil", i))
		}
	}
	combined = append(combined, sources...)
	return combined
}

type skillSliceSource struct {
	skills []*Skill
}

func newSkillSliceSource(skills ...*Skill) *skillSliceSource {
	cloned := append([]*Skill(nil), skills...)
	for i, skill := range cloned {
		if skill == nil {
			panic(fmt.Sprintf("skill %d is nil", i))
		}
		if err := skill.Frontmatter.Validate(); err != nil {
			panic(fmt.Sprintf("skill %d has invalid frontmatter: %v", i, err))
		}
	}
	return &skillSliceSource{skills: cloned}
}

func (s *skillSliceSource) Skills(context.Context) ([]*Skill, error) {
	return s.skills, nil
}

func (p *providerState) provide(ctx context.Context, messages []*message.Message, options ...agent.Option) (outMessages []*message.Message, outOptions []agent.Option, err error) {
	if p.options.DisableCaching {
		result, err := p.buildContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		outMessages, outOptions = extendProviderContext(messages, options, result)
		return outMessages, outOptions, nil
	}

	p.mu.Lock()
	if p.cached != nil {
		cached := *p.cached
		p.mu.Unlock()
		outMessages, outOptions = extendProviderContext(messages, options, cached)
		return outMessages, outOptions, nil
	}
	if p.loading != nil {
		loading := p.loading
		p.mu.Unlock()
		<-loading

		p.mu.Lock()
		defer p.mu.Unlock()
		if p.cached == nil {
			result, err := p.buildContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			outMessages, outOptions = extendProviderContext(messages, options, result)
			return outMessages, outOptions, nil
		}
		cached := *p.cached
		outMessages, outOptions = extendProviderContext(messages, options, cached)
		return outMessages, outOptions, nil
	}
	p.loading = make(chan struct{})
	loading := p.loading
	p.mu.Unlock()

	var result providerContext
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("building skills context panicked: %v", recovered)
		}

		p.mu.Lock()
		if err == nil {
			cached := result
			p.cached = &cached
		}
		close(loading)
		p.loading = nil
		p.mu.Unlock()
	}()

	result, err = p.buildContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	outMessages, outOptions = extendProviderContext(messages, options, result)
	return outMessages, outOptions, nil
}

func extendProviderContext(messages []*message.Message, options []agent.Option, result providerContext) ([]*message.Message, []agent.Option) {
	outMessages := messages
	if len(result.Messages) > 0 {
		outMessages = append(messages, result.Messages...)
	}

	outOptions := options
	if len(result.Options) > 0 {
		outOptions = append(options, result.Options...)
	}
	if strings.TrimSpace(result.Instructions) != "" {
		outOptions = append(outOptions, agent.WithInstructions(result.Instructions))
	}

	return outMessages, outOptions
}

func (p *providerState) buildContext(ctx context.Context) (providerContext, error) {
	skills, err := p.loadSkills(ctx)
	if err != nil {
		return providerContext{}, err
	}
	if len(skills) == 0 {
		return providerContext{}, nil
	}

	indexed := indexSkills(skills)
	out := providerContext{
		Options: toolOptions(p.buildTools(indexed)),
	}

	instructions := buildProviderSkillsInstructionPrompt(p.options.SkillsInstructionPrompt, skills)
	if instructions != "" {
		out.Instructions = instructions
	}

	return out, nil
}

func toolOptions(tools []tool.Tool) []agent.Option {
	if len(tools) == 0 {
		return nil
	}
	options := make([]agent.Option, 0, len(tools))
	for _, tool := range tools {
		options = append(options, agent.WithTool(tool))
	}
	return options
}

func (p *providerState) loadSkills(ctx context.Context) ([]*Skill, error) {
	loaded := make([]*Skill, 0)
	for sourceIndex, source := range p.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceSkills, err := source.Skills(ctx)
		if err != nil {
			return nil, err
		}
		for skillIndex, skill := range sourceSkills {
			if skill == nil {
				p.logger.Warn("Skipping nil skill returned by source", "sourceIndex", sourceIndex, "skillIndex", skillIndex)
				continue
			}
			if err := skill.Frontmatter.Validate(); err != nil {
				p.logger.Warn("Skipping skill with invalid frontmatter", "sourceIndex", sourceIndex, "skillIndex", skillIndex, "error", err)
				continue
			}
			if p.options.SkillFilter != nil && !p.options.SkillFilter(skill) {
				p.logger.Debug("Skill excluded by filter predicate", "skillName", skill.Frontmatter.Name, "sourceIndex", sourceIndex, "skillIndex", skillIndex)
				continue
			}
			loaded = append(loaded, skill)
		}
	}
	if p.options.DisableSourceDeduplication {
		return loaded, nil
	}
	return deduplicateSkillsByName(loaded, p.logger), nil
}

func deduplicateSkillsByName(skills []*Skill, logger *slog.Logger) []*Skill {
	seen := make(map[string]struct{}, len(skills))
	deduplicated := skills[:0]
	for _, skill := range skills {
		resolvedKey := strings.ToLower(skill.Frontmatter.Name)
		if _, ok := seen[resolvedKey]; ok {
			logger.Warn("Duplicate skill name: subsequent skill skipped in favor of first occurrence", "skillName", skill.Frontmatter.Name)
			continue
		}
		seen[resolvedKey] = struct{}{}
		deduplicated = append(deduplicated, skill)
	}
	return deduplicated
}

func indexSkills(skills []*Skill) providedSkillSet {
	indexed := make(map[string]providedSkill, len(skills))
	for _, skill := range skills {
		resources := make(map[string]Resource)
		for _, resource := range skill.Resources {
			resources[resource.Name] = resource
		}
		scripts := make(map[string]Script)
		for _, script := range skill.Scripts {
			scripts[script.Name] = script
		}
		indexed[skill.Frontmatter.Name] = providedSkill{
			skill:     skill,
			resources: resources,
			scripts:   scripts,
		}
	}
	return providedSkillSet{byName: indexed}
}

func (p *providerState) buildTools(skills providedSkillSet) []tool.Tool {
	tools := []tool.Tool{
		functool.MustNew(
			functool.Config{
				Name:        "load_skill",
				Description: "Loads the full content of a specific skill.",
			},
			func(callCtx context.Context, in struct {
				SkillName string `json:"skillName" jsonschema:"The name of the skill to load"`
			},
			) (string, error) {
				return p.loadSkill(callCtx, skills, in.SkillName)
			},
		),
		functool.MustNew(
			functool.Config{
				Name:        "read_skill_resource",
				Description: "Reads a resource associated with a skill, such as references, assets, or dynamic data.",
			},
			func(callCtx context.Context, in struct {
				SkillName    string `json:"skillName" jsonschema:"The name of the skill"`
				ResourceName string `json:"resourceName" jsonschema:"The exact resource name to read"`
			},
			) (any, error) {
				return p.readSkillResource(callCtx, skills, in.SkillName, in.ResourceName), nil
			},
		),
	}

	runScript := functool.MustNew(
		functool.Config{
			Name:        "run_skill_script",
			Description: "Runs a script associated with a skill.",
		},
		func(callCtx context.Context, in struct {
			SkillName  string   `json:"skillName" jsonschema:"The name of the skill"`
			ScriptName string   `json:"scriptName" jsonschema:"The exact script name to run"`
			Arguments  []string `json:"arguments,omitempty" jsonschema:"Positional CLI-style string arguments for the script, e.g. [\"--value\",\"26.2\",\"--factor\",\"1.60934\"]"`
		},
		) (any, error) {
			return p.runSkillScript(callCtx, skills, in.SkillName, in.ScriptName, in.Arguments), nil
		},
	)

	if p.options.ScriptApproval {
		return append(tools, tool.ApprovalRequiredFunc(runScript))
	}
	return append(tools, runScript)
}

func (p *providerState) loadSkill(ctx context.Context, skills providedSkillSet, skillName string) (string, error) {
	resolved, lookupError := skills.resolveSkill(skillName)
	if lookupError != "" {
		return lookupError, nil
	}
	p.logger.Info("Loading skill", "skillName", resolved.skill.Frontmatter.Name)
	if resolved.skill.GetContent == nil {
		p.logger.Error("Failed to load skill content", "skillName", skillName, "error", errors.New("skill content loader is nil"))
		return fmt.Sprintf("Error: Failed to load skill '%s'.", skillName), nil
	}
	content, err := resolved.skill.GetContent(ctx)
	if err != nil {
		p.logger.Error("Failed to load skill content", "skillName", skillName, "error", err)
		return fmt.Sprintf("Error: Failed to load skill '%s'.", skillName), nil
	}
	return content, nil
}

func (p *providerState) readSkillResource(ctx context.Context, skills providedSkillSet, skillName, resourceName string) any {
	if lookupError := validateSkillName(skillName); lookupError != "" {
		return lookupError
	}
	if strings.TrimSpace(resourceName) == "" {
		return "Error: Resource name cannot be empty."
	}
	resolved, lookupError := skills.lookupSkill(skillName)
	if lookupError != "" {
		return lookupError
	}
	resource, ok := resolved.resources[resourceName]
	if !ok {
		return fmt.Sprintf("Error: Resource '%s' not found in skill '%s'.", resourceName, skillName)
	}
	if resource.Read == nil {
		p.logger.Error("Failed to read resource from skill", "resourceName", resourceName, "skillName", skillName, "error", "resource reader is nil")
		return fmt.Sprintf("Error: Failed to read resource '%s' from skill '%s'.", resourceName, skillName)
	}
	content, err := resource.Read(ctx)
	if err != nil {
		p.logger.Error("Failed to read resource from skill", "resourceName", resourceName, "skillName", skillName, "error", err)
		return fmt.Sprintf("Error: Failed to read resource '%s' from skill '%s'.", resourceName, skillName)
	}
	return content
}

func (p *providerState) runSkillScript(ctx context.Context, skills providedSkillSet, skillName, scriptName string, arguments []string) any {
	if lookupError := validateSkillName(skillName); lookupError != "" {
		return lookupError
	}
	if strings.TrimSpace(scriptName) == "" {
		return "Error: Script name cannot be empty."
	}
	resolved, lookupError := skills.lookupSkill(skillName)
	if lookupError != "" {
		return lookupError
	}
	script, ok := resolved.scripts[scriptName]
	if !ok {
		return fmt.Sprintf("Error: Script '%s' not found in skill '%s'.", scriptName, skillName)
	}
	if script.Run == nil {
		p.logger.Error("Failed to execute script from skill", "scriptName", scriptName, "skillName", skillName, "error", "script runner is nil")
		return fmt.Sprintf("Error: Failed to execute script '%s' from skill '%s'.", scriptName, skillName)
	}
	result, err := script.Run(ctx, resolved.skill, arguments)
	if err != nil {
		p.logger.Error("Failed to execute script from skill", "scriptName", scriptName, "skillName", skillName, "error", err)
		return fmt.Sprintf("Error: Failed to execute script '%s' from skill '%s'.", scriptName, skillName)
	}
	return result
}

func (skills providedSkillSet) resolveSkill(skillName string) (providedSkill, string) {
	if lookupError := validateSkillName(skillName); lookupError != "" {
		return providedSkill{}, lookupError
	}
	return skills.lookupSkill(skillName)
}

func (skills providedSkillSet) lookupSkill(skillName string) (providedSkill, string) {
	resolved, ok := skills.byName[skillName]
	if !ok {
		return providedSkill{}, fmt.Sprintf("Error: Skill '%s' not found.", skillName)
	}
	return resolved, ""
}

func validateSkillName(skillName string) string {
	if strings.TrimSpace(skillName) == "" {
		return "Error: Skill name cannot be empty."
	}
	return ""
}

func buildProviderSkillsInstructionPrompt(template string, skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	if template == "" {
		template = defaultSkillsInstructionPrompt
	}

	sortedSkills := append([]*Skill(nil), skills...)
	slices.SortFunc(sortedSkills, func(left, right *Skill) int {
		return strings.Compare(left.Frontmatter.Name, right.Frontmatter.Name)
	})

	var sb strings.Builder
	for _, skill := range sortedSkills {
		sb.WriteString("  <skill>\n")
		_, _ = fmt.Fprintf(&sb, "    <name>%s</name>\n", xmlEscape(skill.Frontmatter.Name))
		_, _ = fmt.Fprintf(&sb, "    <description>%s</description>\n", xmlEscape(skill.Frontmatter.Description))
		sb.WriteString("  </skill>\n")
	}

	skillList := strings.TrimRight(sb.String(), "\n")
	resourceInstruction := "- Use `read_skill_resource` to read any referenced resources, using the name exactly as listed\n   (e.g. `\"style-guide\"` not `\"style-guide.md\"`, `\"references/FAQ.md\"` not `\"FAQ.md\"`)."
	scriptInstruction := "- Use `run_skill_script` to run referenced scripts, using the name exactly as listed."

	replacer := strings.NewReplacer(
		skillsPlaceholder, skillList,
		resourceInstructionsPlaceholder, resourceInstruction,
		scriptInstructionsPlaceholder, scriptInstruction,
	)
	return replacer.Replace(template)
}

func validatePromptTemplate(template string) error {
	for _, placeholder := range []string{skillsPlaceholder, resourceInstructionsPlaceholder, scriptInstructionsPlaceholder} {
		if !strings.Contains(template, placeholder) {
			return fmt.Errorf("custom prompt template must contain the %q placeholder", placeholder)
		}
	}
	return nil
}

func xmlEscape(s string) string {
	var sb strings.Builder
	if err := xml.EscapeText(&sb, []byte(s)); err != nil {
		return s
	}
	return sb.String()
}
