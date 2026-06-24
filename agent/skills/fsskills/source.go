// Copyright (c) Microsoft. All rights reserved.

package fsskills

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/agent/skills"
)

const (
	skillFileName      = "SKILL.md"
	defaultSearchDepth = 2

	rootFSPropertyKey = "fsskills.rootFS"

	// defaultFileScriptSchema is the JSON schema for file-based script arguments.
	// File-based scripts receive a positional CLI-style string array, matching
	// the .NET AgentFileSkillScript default schema.
	defaultFileScriptSchema = `{"type":"array","items":{"type":"string"}}`
)

var (
	defaultResourceExtensions = []string{".md", ".json", ".yaml", ".yml", ".csv", ".xml", ".txt"}
	defaultScriptExtensions   = []string{".py", ".js", ".sh", ".ps1", ".cs", ".csx"}
)

var (
	frontmatterRegex          = regexp.MustCompile(`(?ms)\A^---\s*$(.+?)^---\s*$`)
	yamlKeyValueRegex         = regexp.MustCompile(`(?m)^([\w-]+)\s*:\s*(?:["'](.+?)["']|(.+?))\s*$`)
	yamlMetadataBlockRegex    = regexp.MustCompile(`(?m)^metadata\s*:\s*$\n((?:[ \t]+\S.*\n?)+)`)
	yamlIndentedKeyValueRegex = regexp.MustCompile(`(?m)^\s+([\w-]+)\s*:\s*(?:["'](.+?)["']|(.+?))\s*$`)
)

// FilterContext provides contextual information about a discovered file to the
// ScriptFilter and ResourceFilter predicates.
type FilterContext struct {
	// SkillName is the name of the skill as declared in the SKILL.md frontmatter.
	SkillName string

	// RelativeFilePath is the path to the file relative to the skill directory,
	// using forward slashes. For root-level files this is just the filename;
	// for nested files it includes the subdirectory (e.g. "scripts/run.py").
	RelativeFilePath string
}

// SourceOptions configures file-based skill discovery.
//
// Use this struct to configure discovery without relying on positional
// constructor parameters. Additional options can be added here without
// changing the zero-options NewSource convenience API.
type SourceOptions struct {
	// SearchDepth controls the maximum depth to search for resource and script
	// files within each skill directory. A value of 1 searches only the skill
	// root; a value of 2 (the default) also searches one level of subdirectories.
	// Values less than 1 are treated as the default.
	SearchDepth int

	// ResourceFilter is an optional predicate applied to each candidate resource
	// file after extension filtering. Return true to include the file or false to
	// skip it. When nil, all files matching AllowedResourceExtensions are included.
	ResourceFilter func(FilterContext) bool

	// ScriptFilter is an optional predicate applied to each candidate script file
	// after extension filtering. Return true to include the file or false to skip
	// it. When nil, all files matching AllowedScriptExtensions are included.
	ScriptFilter func(FilterContext) bool

	// AllowedResourceExtensions specifies the allowed file extensions for skill
	// resources.
	//
	// When nil, defaults to ".md", ".json", ".yaml", ".yml", ".csv",
	// ".xml", and ".txt".
	AllowedResourceExtensions []string

	// AllowedScriptExtensions specifies the allowed file extensions for skill
	// scripts.
	//
	// When nil, defaults to ".py", ".js", ".sh", ".ps1", ".cs", and
	// ".csx".
	AllowedScriptExtensions []string

	// ScriptRunner executes discovered file-backed scripts. When nil, scripts are
	// still discovered but return an error if run.
	ScriptRunner skills.ScriptRunner

	// Logger is used for discovery diagnostics. When nil, a discard logger is
	// used.
	Logger *slog.Logger
}

// Source discovers file-based skills from one or more filesystems.
//
// A Source applies the normalized directory and extension configuration from
// SourceOptions across all provided filesystems and materializes discovered
// skills as skills.Skill values.
type Source struct {
	filesystems               []fs.FS
	logger                    *slog.Logger
	searchDepth               int
	resourceFilter            func(FilterContext) bool
	scriptFilter              func(FilterContext) bool
	allowedResourceExtensions map[string]bool
	allowedScriptExtensions   map[string]bool
	scriptRunner              skills.ScriptRunner
}

// FSFromSkill returns the root filesystem for a file-backed skill.
func FSFromSkill(skill *skills.Skill) (fs.FS, error) {
	if skill == nil {
		return nil, fmt.Errorf("skill is required")
	}
	if skill.AdditionalProperties == nil {
		return nil, fmt.Errorf("skill %q does not have a backing fs.FS", skill.Frontmatter.Name)
	}
	root, ok := skill.AdditionalProperties[rootFSPropertyKey].(fs.FS)
	if !ok || root == nil {
		return nil, fmt.Errorf("skill %q does not have a backing fs.FS", skill.Frontmatter.Name)
	}
	return root, nil
}

// NewSource creates a file-based skill source with default options.
func NewSource(filesystems ...fs.FS) *Source {
	return NewSourceOptions(SourceOptions{}, filesystems...)
}

// NewSourceOptions creates a file-based skill source using the provided options.
func NewSourceOptions(opts SourceOptions, filesystems ...fs.FS) *Source {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	searchDepth := opts.SearchDepth
	if searchDepth < 1 {
		searchDepth = defaultSearchDepth
	}
	return &Source{
		filesystems:               append([]fs.FS(nil), filesystems...),
		logger:                    logger,
		searchDepth:               searchDepth,
		resourceFilter:            opts.ResourceFilter,
		scriptFilter:              opts.ScriptFilter,
		allowedResourceExtensions: buildExtensionSet(opts.AllowedResourceExtensions, defaultResourceExtensions),
		allowedScriptExtensions:   buildExtensionSet(opts.AllowedScriptExtensions, defaultScriptExtensions),
		scriptRunner:              opts.ScriptRunner,
	}
}

// Skills discovers and loads valid skills from the configured filesystems.
func (s *Source) Skills(ctx context.Context) ([]*skills.Skill, error) {
	directories := discoverSkillDirectories(s.filesystems)
	s.logger.Info("Discovered potential skills", "count", len(directories))

	skills := make([]*skills.Skill, 0, len(directories))
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		skill := s.parseSkillDirectory(directory.fsys, directory.path)
		if skill == nil {
			continue
		}
		skills = append(skills, skill)
		s.logger.Info("Loaded skill", "skillName", skill.Frontmatter.Name)
	}

	s.logger.Info("Successfully loaded skills", "count", len(skills))
	return skills, nil
}

func discoverSkillDirectories(filesystems []fs.FS) []discoveredSkillDir {
	var results []discoveredSkillDir
	for _, filesystem := range filesystems {
		searchForSkills(filesystem, ".", &results, 0)
	}
	return results
}

func searchForSkills(filesystem fs.FS, dir string, results *[]discoveredSkillDir, currentDepth int) {
	skillPath := path.Join(dir, skillFileName)
	if _, err := fs.Stat(filesystem, skillPath); err == nil {
		sub := filesystem
		var subErr error
		if dir != "." {
			sub, subErr = fs.Sub(filesystem, dir)
		}
		if subErr == nil {
			*results = append(*results, discoveredSkillDir{fsys: sub, path: dir})
			return
		}
	}
	if currentDepth >= defaultSearchDepth {
		return
	}
	entries, err := fs.ReadDir(filesystem, dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			searchForSkills(filesystem, path.Join(dir, entry.Name()), results, currentDepth+1)
		}
	}
}

func (s *Source) parseSkillDirectory(skillFS fs.FS, logPath string) *skills.Skill {
	data, err := fs.ReadFile(skillFS, skillFileName)
	if err != nil {
		s.logger.Error("Failed to read SKILL.md", "path", logPath, "error", err)
		return nil
	}
	content := string(data)

	frontmatter, ok := s.tryParseFrontmatter(content, logPath)
	if !ok {
		return nil
	}

	resources := s.discoverResourceFiles(skillFS, frontmatter.Name)
	scripts := s.discoverScriptFiles(skillFS, frontmatter.Name)
	var (
		contentOnce   sync.Once
		cachedContent string
		contentErr    error
	)
	return &skills.Skill{
		Frontmatter: frontmatter,
		GetContent: func(context.Context) (string, error) {
			contentOnce.Do(func() {
				data, err := fs.ReadFile(skillFS, skillFileName)
				if err != nil {
					contentErr = err
					return
				}
				raw := string(data)
				if schemasBlock := buildScriptSchemasBlock(scripts); schemasBlock != "" {
					raw += schemasBlock
				}
				cachedContent = raw
			})
			return cachedContent, contentErr
		},
		Resources: resources,
		Scripts:   scripts,
		AdditionalProperties: map[string]any{
			rootFSPropertyKey: skillFS,
		},
	}
}

func (s *Source) tryParseFrontmatter(content, skillFilePath string) (skills.Frontmatter, bool) {
	contentForParsing := strings.TrimPrefix(content, "\uFEFF")
	match := frontmatterRegex.FindStringSubmatchIndex(contentForParsing)
	if match == nil {
		s.logger.Error("SKILL.md does not contain valid YAML frontmatter delimited by '---'", "skillFilePath", skillFilePath)
		return skills.Frontmatter{}, false
	}

	yamlContent := strings.TrimSpace(contentForParsing[match[2]:match[3]])
	frontmatter := skills.Frontmatter{}

	for _, kv := range yamlKeyValueRegex.FindAllStringSubmatchIndex(yamlContent, -1) {
		key := yamlContent[kv[2]:kv[3]]
		value := ""
		if kv[4] >= 0 {
			value = yamlContent[kv[4]:kv[5]]
		} else if kv[6] >= 0 {
			value = parseYamlScalarValue(yamlContent, kv)
		}
		switch strings.ToLower(key) {
		case "name":
			frontmatter.Name = value
		case "description":
			frontmatter.Description = value
		case "license":
			frontmatter.License = value
		case "compatibility":
			frontmatter.Compatibility = value
		case "allowed-tools":
			frontmatter.AllowedTools = value
		}
	}

	if metadataMatch := yamlMetadataBlockRegex.FindStringSubmatch(yamlContent); len(metadataMatch) == 2 {
		metadata := make(map[string]any)
		for _, kv := range yamlIndentedKeyValueRegex.FindAllStringSubmatch(metadataMatch[1], -1) {
			value := kv[2]
			if value == "" {
				value = kv[3]
			}
			metadata[kv[1]] = value
		}
		if len(metadata) > 0 {
			frontmatter.Metadata = metadata
		}
	}

	if err := frontmatter.Validate(); err != nil {
		s.logger.Error("SKILL.md has invalid frontmatter", "skillFilePath", skillFilePath, "error", err)
		return skills.Frontmatter{}, false
	}

	if skillFilePath != "." {
		directoryName := path.Base(skillFilePath)
		if frontmatter.Name != directoryName {
			s.logger.Error("SKILL.md name does not match skill directory name", "skillFilePath", skillFilePath, "name", frontmatter.Name, "directoryName", directoryName)
			return skills.Frontmatter{}, false
		}
	}

	return frontmatter, true
}

func parseYamlScalarValue(yamlContent string, kv []int) string {
	value := yamlContent[kv[6]:kv[7]]
	if value == "" || (value[0] != '|' && value[0] != '>') {
		return value
	}

	scalarStyle := value[0]
	keepTrailingNewline := len(value) > 1 && value[1] == '+'
	lineBreak := strings.IndexByte(yamlContent[kv[1]:], '\n')
	if lineBreak < 0 {
		return value
	}

	remaining := yamlContent[kv[1]+lineBreak+1:]
	if before, ok := strings.CutSuffix(remaining, "\n"); ok {
		remaining = before
	}

	var blockLines []string
	for line := range strings.SplitSeq(remaining, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			blockLines = append(blockLines, "")
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
		blockLines = append(blockLines, line)
	}

	if len(blockLines) == 0 {
		return ""
	}

	commonIndent := -1
	for _, line := range blockLines {
		if line == "" {
			continue
		}
		indent := leadingWhitespaceCount(line)
		if commonIndent < 0 || indent < commonIndent {
			commonIndent = indent
		}
	}
	if commonIndent < 0 {
		commonIndent = 0
	}

	normalizedLines := make([]string, len(blockLines))
	for i, line := range blockLines {
		if line == "" {
			continue
		}
		if commonIndent < len(line) {
			normalizedLines[i] = line[commonIndent:]
		}
	}

	var parsedValue string
	if scalarStyle == '|' {
		parsedValue = strings.Join(normalizedLines, "\n")
	} else {
		parsedValue = foldYamlLines(normalizedLines)
	}

	if keepTrailingNewline {
		return parsedValue + "\n"
	}
	return parsedValue
}

func foldYamlLines(lines []string) string {
	var builder strings.Builder
	blankLines := 0
	for _, line := range lines {
		if line == "" {
			blankLines++
			continue
		}

		if builder.Len() > 0 {
			if blankLines > 0 {
				builder.WriteString(strings.Repeat("\n", blankLines))
			} else {
				builder.WriteByte(' ')
			}
		}
		builder.WriteString(line)
		blankLines = 0
	}
	return builder.String()
}

func leadingWhitespaceCount(line string) int {
	count := 0
	for count < len(line) && (line[count] == ' ' || line[count] == '\t') {
		count++
	}
	return count
}

func (s *Source) discoverResourceFiles(skillFS fs.FS, skillName string) []skills.Resource {
	seen := make(map[string]bool)
	var resources []skills.Resource
	s.scanForFiles(skillFS, ".", skillName, 1, s.allowedResourceExtensions, s.resourceFilter, "resource", func(filePath string) {
		key := strings.ToLower(filePath)
		if seen[key] {
			return
		}
		seen[key] = true
		resources = append(resources, skills.Resource{
			Name: filePath,
			Read: func(context.Context) (any, error) {
				data, err := fs.ReadFile(skillFS, filePath)
				if err != nil {
					return nil, err
				}
				return string(data), nil
			},
		})
	})
	return resources
}

func (s *Source) discoverScriptFiles(skillFS fs.FS, skillName string) []skills.Script {
	seen := make(map[string]bool)
	var scripts []skills.Script
	s.scanForFiles(skillFS, ".", skillName, 1, s.allowedScriptExtensions, s.scriptFilter, "script", func(filePath string) {
		key := strings.ToLower(filePath)
		if seen[key] {
			return
		}
		seen[key] = true
		scripts = append(scripts, newScript(filePath, skillFS, s.scriptRunner))
	})
	return scripts
}

// scanForFiles recursively scans the skill filesystem up to searchDepth levels deep,
// collecting files matching allowedExtensions and the optional predicate filter.
func (s *Source) scanForFiles(
	skillFS fs.FS,
	dir string,
	skillName string,
	currentDepth int,
	allowedExtensions map[string]bool,
	filter func(FilterContext) bool,
	kind string,
	collect func(string),
) {
	if currentDepth > s.searchDepth {
		return
	}

	entries, err := fs.ReadDir(skillFS, dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Warn("Failed to read skill directory", "skillName", skillName, "directory", dir, "kind", kind, "error", err)
		}
		return
	}

	for _, entry := range entries {
		entryPath := path.Join(dir, entry.Name())
		if entry.IsDir() {
			if currentDepth < s.searchDepth {
				s.scanForFiles(skillFS, entryPath, skillName, currentDepth+1, allowedExtensions, filter, kind, collect)
			}
			continue
		}

		// Exclude SKILL.md itself
		if strings.EqualFold(entry.Name(), skillFileName) {
			continue
		}

		ext := strings.ToLower(path.Ext(entry.Name()))
		if ext == "" || !allowedExtensions[ext] {
			if ext == "" {
				s.logger.Debug("Skipping file: extension not in the allowed list", "skillName", skillName, "filePath", entryPath, "extension", "(none)", "kind", kind)
			} else {
				s.logger.Debug("Skipping file: extension not in the allowed list", "skillName", skillName, "filePath", entryPath, "extension", ext, "kind", kind)
			}
			continue
		}

		relativePath := entryPath

		if filter != nil && !filter(FilterContext{SkillName: skillName, RelativeFilePath: relativePath}) {
			continue
		}

		collect(relativePath)
	}
}

func buildExtensionSet(extensions []string, defaults []string) map[string]bool {
	if extensions == nil {
		extensions = defaults
	}
	validateExtensions(extensions)
	set := make(map[string]bool, len(extensions))
	for _, extension := range extensions {
		set[strings.ToLower(extension)] = true
	}
	return set
}

func validateExtensions(extensions []string) {
	for _, extension := range extensions {
		if extension == "" || extension[0] != '.' {
			panic(fmt.Sprintf("invalid extension %q: must start with '.'", extension))
		}
	}
}

func newScript(name string, fsys fs.FS, runner skills.ScriptRunner) skills.Script {
	return skills.Script{
		Name:             name,
		ParametersSchema: defaultFileScriptSchema,
		Run: func(ctx context.Context, owner *skills.Skill, arguments []string) (any, error) {
			if _, err := FSFromSkill(owner); err != nil {
				return nil, fmt.Errorf("file-based script %q requires a skill with a backing fs.FS: %w", name, err)
			}
			if runner == nil {
				return nil, fmt.Errorf("script %q cannot be executed because no file script runner was provided", name)
			}
			script := &skills.Script{Name: name}
			return runner(ctx, owner, script, arguments)
		},
		AdditionalProperties: map[string]any{
			"fsskills.scriptFS": fsys,
		},
	}
}

type discoveredSkillDir struct {
	fsys fs.FS
	path string
}

// buildScriptSchemasBlock returns a <script_schemas> XML block listing each
// script with its parameter schema. Scripts with no schema emit a self-closing
// element; scripts with a schema emit the JSON inline.
// Returns an empty string when scripts is empty.
func buildScriptSchemasBlock(scripts []skills.Script) string {
	if len(scripts) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n<script_schemas>\n")
	for _, script := range scripts {
		if script.ParametersSchema == "" {
			fmt.Fprintf(&sb, "  <schema script=\"%s\"/>\n", xmlEscapeAttr(script.Name))
		} else {
			fmt.Fprintf(&sb, "  <schema script=\"%s\">%s</schema>\n", xmlEscapeAttr(script.Name), xmlEscapeContent(script.ParametersSchema))
		}
	}
	sb.WriteString("</script_schemas>")
	return sb.String()
}

// xmlEscapeAttr escapes a string for use in an XML attribute value (double-quoted).
func xmlEscapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// xmlEscapeContent escapes a string for use as XML element content.
// Quotes are intentionally preserved to keep embedded JSON readable.
func xmlEscapeContent(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
