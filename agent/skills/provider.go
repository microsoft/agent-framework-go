// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"cmp"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	loadSkillToolName         = "load_skill"
	readSkillResourceToolName = "read_skill_resource"
	runSkillScriptToolName    = "run_skill_script"
	skillsPlaceholder         = "{skills}"
)

const defaultSkillsInstructionPrompt = `You have access to skills containing domain-specific knowledge and capabilities.
Each skill provides specialized instructions, reference documents, and assets for specific tasks.

<available_skills>
{skills}
</available_skills>

When a task aligns with a skill's domain, follow these steps in exact order:
- Use ` + "`load_skill`" + ` to retrieve the skill's instructions.
- Follow the provided guidance.
- Use ` + "`read_skill_resource`" + ` to read any referenced resources, using the name exactly as listed
   (e.g. ` + "`\"style-guide\"` not `\"style-guide.md\"`, `\"references/FAQ.md\"` not `\"FAQ.md\"`" + `).
- Use ` + "`run_skill_script`" + ` to run referenced scripts, using the name exactly as listed.
Only load what is needed, when it is needed.`

// ReadOnlyToolsAutoApprovalRule auto-approves read-only skill tools.
//
// Add this function to toolapproval.Config.AutoApprovalRules to automatically
// approve load_skill and read_skill_resource while continuing to prompt for
// run_skill_script and non-skill tools.
func ReadOnlyToolsAutoApprovalRule(_ context.Context, ruleContext *toolapproval.ToolAutoApprovalRuleContext) (bool, error) {
	if ruleContext == nil || ruleContext.FunctionCall == nil {
		return false, nil
	}
	switch ruleContext.FunctionCall.Name {
	case loadSkillToolName, readSkillResourceToolName:
		return true, nil
	default:
		return false, nil
	}
}

// AllToolsAutoApprovalRule auto-approves all skill tools.
//
// Add this function to toolapproval.Config.AutoApprovalRules to automatically
// approve load_skill, read_skill_resource, and run_skill_script.
// Warning: only use this rule when skill scripts are trusted, because it
// auto-approves script execution.
func AllToolsAutoApprovalRule(_ context.Context, ruleContext *toolapproval.ToolAutoApprovalRuleContext) (bool, error) {
	if ruleContext == nil || ruleContext.FunctionCall == nil {
		return false, nil
	}
	switch ruleContext.FunctionCall.Name {
	case loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName:
		return true, nil
	default:
		return false, nil
	}
}

// ContextProviderOptions configures a skills-backed agent.ContextProvider.
type ContextProviderOptions struct {
	// SourceID is the identifier for the provider's source in the resulting context.
	// Defaults to "skills" if not provided.
	SourceID string

	// SkillFilter optionally filters skills loaded from inline skills and sources.
	// Returning true keeps a skill; returning false excludes it.
	// It receives the current agent/session context and is evaluated on every
	// invocation after source caching and before deduplication.
	SkillFilter func(context.Context, *Skill, SourceContext) bool

	// Skills provides in-memory skills to register with the provider.
	Skills []*Skill

	// Sources provides external skill sources to register with the provider.
	Sources []Source

	// OwnsSources transfers responsibility for closing sources that implement
	// [io.Closer] to the provider. The default is false.
	OwnsSources bool

	// SkillsInstructionPrompt is a custom system prompt template.
	// When nil, a default template is used.
	//
	// The template must contain {skills}.
	SkillsInstructionPrompt *string

	// DisableLoadSkillApproval disables approval for the load_skill tool.
	// When false (the default), invoking load_skill requires approval.
	DisableLoadSkillApproval bool

	// DisableReadSkillResourceApproval disables approval for the
	// read_skill_resource tool. When false (the default), invoking
	// read_skill_resource requires approval.
	DisableReadSkillResourceApproval bool

	// DisableRunSkillScriptApproval disables approval for the
	// run_skill_script tool. When false (the default), invoking
	// run_skill_script requires approval.
	DisableRunSkillScriptApproval bool

	// IncludeDetailedErrors includes script execution error details in the
	// run_skill_script result returned to the model.
	//
	// When false, script execution errors are returned to the caller so tool
	// invocation middleware can apply its own error-detail policy. When true,
	// the exception message is appended to the tool result so the model can
	// retry with different arguments. Only enable this for trusted skills and
	// scripts because raw error messages may contain prompt-injection content.
	IncludeDetailedErrors bool

	// DisableCaching reloads sources on every invocation. Instructions and
	// tools are always assembled per invocation, after filtering.
	DisableCaching bool

	// CacheIsolationKeySelector selects the cache bucket for an invocation.
	// A nil selector, or a nil key returned by the selector, uses the shared
	// cache bucket. Keys should be stable and low-cardinality.
	CacheIsolationKeySelector func(context.Context, SourceContext) *string

	// RefreshInterval controls how long a cached result remains fresh. A nil
	// interval never expires the cache. A zero or negative interval refreshes
	// on every invocation.
	RefreshInterval *time.Duration

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

func newProvidedSkill(skill *Skill) providedSkill {
	resources := make(map[string]Resource, len(skill.Resources))
	for _, resource := range skill.Resources {
		resources[resource.Name] = resource
	}
	scripts := make(map[string]Script, len(skill.Scripts))
	for _, script := range skill.Scripts {
		scripts[script.Name] = script
	}
	return providedSkill{
		skill:     skill,
		resources: resources,
		scripts:   scripts,
	}
}

func (ps providedSkill) lookupResource(name string) (Resource, bool) {
	resource, ok := ps.resources[name]
	return resource, ok
}

func (ps providedSkill) lookupScript(name string) (Script, bool) {
	script, ok := ps.scripts[name]
	return script, ok
}

type providedSkillSet struct {
	byName map[string]providedSkill
}

// ContextProvider supplies skill instructions and tools to an agent. Create it
// with [NewContextProvider] and call [ContextProvider.Close] when finished.
type ContextProvider struct {
	source   Source
	options  ContextProviderOptions
	logger   *slog.Logger
	provider agent.ContextProvider

	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

type providerContext struct {
	Messages     []*message.Message
	Options      []agent.Option
	Instructions string
}

// NewContextProvider creates a skills context provider from the configured in-memory skills and sources.
// Sources are borrowed unless opts.OwnsSources is true.
func NewContextProvider(opts ContextProviderOptions) *ContextProvider {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.SkillsInstructionPrompt != nil {
		if err := validatePromptTemplate(*opts.SkillsInstructionPrompt); err != nil {
			panic(err)
		}
	}

	sources := newProviderSources(opts.Skills, opts.Sources)
	for i, source := range sources {
		sources[i] = &providerSource{source: source, index: i, logger: opts.Logger, owns: opts.OwnsSources}
	}
	var pipeline Source = NewAggregatingSource(sources...)
	if !opts.DisableCaching {
		pipeline = NewCachingSource(pipeline, CachingSourceConfig{
			CacheIsolationKeySelector: opts.CacheIsolationKeySelector,
			RefreshInterval:           opts.RefreshInterval,
		})
	}
	if opts.SkillFilter != nil {
		pipeline = NewFilteringSource(pipeline, opts.SkillFilter, opts.Logger)
	}
	if !opts.DisableSourceDeduplication {
		pipeline = NewDeduplicatingSource(pipeline, opts.Logger)
	}
	state := &ContextProvider{
		source:  pipeline,
		options: opts,
		logger:  opts.Logger,
	}

	state.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: cmp.Or(opts.SourceID, "skills"),
		Provide:  state.provide,
	})
	return state
}

// Invoking adds skill instructions and tools for the current agent and session.
func (p *ContextProvider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	if err := p.checkOpen(); err != nil {
		return nil, nil, err
	}
	return p.provider.Invoking(ctx, invoking)
}

// Invoked completes a skills-provider invocation without retaining conversation history.
func (p *ContextProvider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	if err := p.checkOpen(); err != nil {
		return err
	}
	return p.provider.Invoked(ctx, invoked)
}

// Close prevents subsequent invocations and tool calls and releases owned
// sources implementing [io.Closer]. It is idempotent, including on an error.
// Do not overlap Close with invocations or tool calls; it does not cancel
// operations already in flight.
func (p *ContextProvider) Close() error {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		if closer, ok := p.source.(io.Closer); ok {
			p.closeErr = closer.Close()
		}
	})
	return p.closeErr
}

func (p *ContextProvider) checkOpen() error {
	if p.closed.Load() {
		return errors.New("skills: provider is closed")
	}
	return nil
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
	cloned := slices.Clone(skills)
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

func (s *skillSliceSource) Skills(_ context.Context, sourceContext SourceContext) ([]*Skill, error) {
	telemetry.MarkUsed(telemetry.FeatureInMemorySkillsSource)
	return s.skills, nil
}

func (p *ContextProvider) provide(ctx context.Context, invoking agent.InvokingContext) (outMessages []*message.Message, outOptions []agent.Option, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outMessages, outOptions = nil, nil
			err = fmt.Errorf("building skills context panicked: %v", recovered)
		}
	}()
	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	a, _ := agent.AgentFromContext(ctx)
	sourceContext := SourceContext{Agent: a, Session: session}
	skills, err := p.source.Skills(ctx, sourceContext)
	if err != nil {
		return nil, nil, err
	}
	outMessages, outOptions = providedContext(p.contextForSkills(skills))
	return outMessages, outOptions, nil
}

func providedContext(result providerContext) ([]*message.Message, []agent.Option) {
	outMessages := result.Messages
	var outOptions []agent.Option
	if len(result.Options) > 0 {
		outOptions = append(outOptions, result.Options...)
	}
	if strings.TrimSpace(result.Instructions) != "" {
		outOptions = append(outOptions, agent.WithInstructions(result.Instructions))
	}

	return outMessages, outOptions
}

func (p *ContextProvider) contextForSkills(skills []*Skill) providerContext {
	if len(skills) == 0 {
		return providerContext{}
	}
	telemetry.MarkUsed(telemetry.FeatureSkillsProvider)
	indexed := indexSkills(skills)
	out := providerContext{
		Options: toolOptions(p.buildTools(indexed)),
	}

	instructions := buildProviderSkillsInstructionPrompt(p.options.SkillsInstructionPrompt, skills)
	if instructions != "" {
		out.Instructions = instructions
	}

	return out
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

type providerSource struct {
	source Source
	index  int
	logger *slog.Logger
	owns   bool
}

func (s *providerSource) Skills(ctx context.Context, sourceContext SourceContext) ([]*Skill, error) {
	skills, err := s.source.Skills(ctx, sourceContext)
	if err != nil {
		return nil, err
	}
	loaded := make([]*Skill, 0)
	for skillIndex, skill := range skills {
		if skill == nil {
			s.logger.Warn("Skipping nil skill returned by source", "sourceIndex", s.index, "skillIndex", skillIndex)
			continue
		}
		if err := skill.Frontmatter.Validate(); err != nil {
			s.logger.Warn("Skipping skill with invalid frontmatter", "sourceIndex", s.index, "skillIndex", skillIndex, "error", err)
			continue
		}
		loaded = append(loaded, skill)
	}
	return loaded, nil
}

func (s *providerSource) Close() error {
	if s.owns {
		if closer, ok := s.source.(io.Closer); ok {
			return closer.Close()
		}
	}
	return nil
}

func indexSkills(skills []*Skill) providedSkillSet {
	indexed := make(map[string]providedSkill, len(skills))
	for _, skill := range skills {
		indexed[skill.Frontmatter.Name] = newProvidedSkill(skill)
	}
	return providedSkillSet{byName: indexed}
}

func (p *ContextProvider) buildTools(skills providedSkillSet) []tool.Tool {
	loadSkillTool := functool.MustNew(
		functool.Config{
			Name:        loadSkillToolName,
			Description: "Loads the full content of a specific skill.",
		},
		func(callCtx context.Context, in struct {
			SkillName string `json:"skillName" jsonschema:"The name of the skill to load"`
		},
		) (string, error) {
			if err := p.checkOpen(); err != nil {
				return "", err
			}
			return p.loadSkill(callCtx, skills, in.SkillName)
		},
	)
	readSkillResourceTool := functool.MustNew(
		functool.Config{
			Name:        readSkillResourceToolName,
			Description: "Reads a resource associated with a skill, such as references, assets, or dynamic data.",
		},
		func(callCtx context.Context, in struct {
			SkillName    string `json:"skillName" jsonschema:"The name of the skill"`
			ResourceName string `json:"resourceName" jsonschema:"The exact resource name to read"`
		},
		) (any, error) {
			if err := p.checkOpen(); err != nil {
				return nil, err
			}
			return p.readSkillResource(callCtx, skills, in.SkillName, in.ResourceName)
		},
	)

	runScript := newSkillScriptTool(p, skills)

	if !p.options.DisableLoadSkillApproval {
		loadSkillTool = tool.ApprovalRequiredFunc(loadSkillTool)
	}
	if !p.options.DisableReadSkillResourceApproval {
		readSkillResourceTool = tool.ApprovalRequiredFunc(readSkillResourceTool)
	}
	if !p.options.DisableRunSkillScriptApproval {
		runScript = tool.ApprovalRequiredFunc(runScript)
	}

	return []tool.Tool{loadSkillTool, readSkillResourceTool, runScript}
}

func (p *ContextProvider) loadSkill(ctx context.Context, skills providedSkillSet, skillName string) (string, error) {
	resolved, lookupError := skills.resolveSkill(skillName)
	if lookupError != "" {
		return lookupError, nil
	}
	p.logger.Info("Loading skill", "skillName", resolved.skill.Frontmatter.Name)
	if resolved.skill.GetContent == nil {
		err := fmt.Errorf("skills: skill %q has no content loader", skillName)
		p.logger.Error("Failed to load skill content", "skillName", skillName, "error", err)
		return "", err
	}
	content, err := resolved.skill.GetContent(ctx)
	if err != nil {
		p.logger.Error("Failed to load skill content", "skillName", skillName, "error", err)
		return "", err
	}
	return content, nil
}

func (p *ContextProvider) readSkillResource(ctx context.Context, skills providedSkillSet, skillName, resourceName string) (any, error) {
	_, resource, lookupError := resolveSkillItem(skills, skillName, resourceName, "Resource", providedSkill.lookupResource)
	if lookupError != "" {
		return lookupError, nil
	}
	if resource.Read == nil {
		err := fmt.Errorf("skills: resource %q in skill %q has no reader", resourceName, skillName)
		p.logger.Error("Failed to read resource from skill", "resourceName", resourceName, "skillName", skillName, "error", err)
		return nil, err
	}
	content, err := resource.Read(ctx)
	if err != nil {
		p.logger.Error("Failed to read resource from skill", "resourceName", resourceName, "skillName", skillName, "error", err)
		return nil, err
	}
	return content, nil
}

func (p *ContextProvider) runSkillScript(ctx context.Context, skills providedSkillSet, skillName, scriptName string, arguments json.RawMessage) (any, error) {
	resolved, script, lookupError := resolveSkillItem(skills, skillName, scriptName, "Script", providedSkill.lookupScript)
	if lookupError != "" {
		return lookupError, nil
	}
	var result any
	var err error
	if script.Run == nil {
		err = errors.New("script runner is nil")
	} else {
		result, err = script.Run(ctx, resolved.skill, arguments)
	}
	if err != nil {
		p.logger.Error("Failed to execute script from skill", "scriptName", scriptName, "skillName", skillName, "error", err)
		if p.options.IncludeDetailedErrors {
			return fmt.Sprintf("Error: Failed to execute script '%s' from skill '%s'. Exception: %s", scriptName, skillName, err.Error()), nil
		}
		return nil, err
	}
	return result, nil
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

func resolveSkillItem[T any](
	skills providedSkillSet,
	skillName, itemName, itemKind string,
	lookup func(providedSkill, string) (T, bool),
) (providedSkill, T, string) {
	var zero T
	if lookupError := validateSkillName(skillName); lookupError != "" {
		return providedSkill{}, zero, lookupError
	}
	if strings.TrimSpace(itemName) == "" {
		return providedSkill{}, zero, fmt.Sprintf("Error: %s name cannot be empty.", itemKind)
	}
	resolved, lookupError := skills.lookupSkill(skillName)
	if lookupError != "" {
		return providedSkill{}, zero, lookupError
	}
	item, ok := lookup(resolved, itemName)
	if !ok {
		return providedSkill{}, zero, fmt.Sprintf("Error: %s '%s' not found in skill '%s'.", itemKind, itemName, skillName)
	}
	return resolved, item, ""
}

func validateSkillName(skillName string) string {
	if strings.TrimSpace(skillName) == "" {
		return "Error: Skill name cannot be empty."
	}
	return ""
}

func buildProviderSkillsInstructionPrompt(template *string, skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	promptTemplate := defaultSkillsInstructionPrompt
	if template != nil {
		promptTemplate = *template
	}

	sortedSkills := slices.Clone(skills)
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
	replacer := strings.NewReplacer(
		skillsPlaceholder, skillList,
	)
	return replacer.Replace(promptTemplate)
}

func validatePromptTemplate(template string) error {
	if !strings.Contains(template, skillsPlaceholder) {
		return fmt.Errorf("custom prompt template must contain the %q placeholder", skillsPlaceholder)
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
