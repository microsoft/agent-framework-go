// Copyright (c) Microsoft. All rights reserved.

package fsskills

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microsoft/agent-framework-go/memory/skills"
)

const (
	skillFileName          = "SKILL.md"
	maxSearchDepth         = 2
	rootDirectoryIndicator = "."

	rootFSPropertyKey = "fsskills.rootFS"
)

var (
	defaultResourceDirectories = []string{"references", "assets"}
	defaultScriptDirectories   = []string{"scripts"}
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

// SourceOptions configures file-based skill discovery.
//
// Use this struct to configure discovery without relying on positional
// constructor parameters. Additional options can be added here without
// changing the zero-options NewSource convenience API.
type SourceOptions struct {
	// ResourceDirectories specifies relative directory paths to scan for resource
	// files within each skill directory.
	//
	// Values may be single-segment names such as "references" or multi-segment
	// relative paths such as "sub/resources". Use "." to include files directly
	// at the skill root. Leading "./" prefixes, trailing separators, and
	// backslashes are normalized automatically; absolute paths and paths
	// containing ".." segments are rejected.
	//
	// When nil, defaults to "references" and "assets" per the Agent Skills
	// specification. When set, replaces the defaults entirely.
	ResourceDirectories []string

	// ScriptDirectories specifies relative directory paths to scan for script
	// files within each skill directory.
	//
	// Values may be single-segment names such as "scripts" or multi-segment
	// relative paths such as "sub/scripts". Use "." to include files directly
	// at the skill root. Leading "./" prefixes, trailing separators, and
	// backslashes are normalized automatically; absolute paths and paths
	// containing ".." segments are rejected.
	//
	// When nil, defaults to "scripts" per the Agent Skills specification. When
	// set, replaces the defaults entirely.
	ScriptDirectories []string

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
	resourceDirectories       []string
	scriptDirectories         []string
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
	return &Source{
		filesystems:               append([]fs.FS(nil), filesystems...),
		logger:                    logger,
		resourceDirectories:       validateAndNormalizeDirectoryNames(opts.ResourceDirectories, defaultResourceDirectories, logger),
		scriptDirectories:         validateAndNormalizeDirectoryNames(opts.ScriptDirectories, defaultScriptDirectories, logger),
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
		sub, err := fs.Sub(filesystem, dir)
		if err == nil {
			*results = append(*results, discoveredSkillDir{fsys: sub, path: dir})
		}
	}
	if currentDepth >= maxSearchDepth {
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
	return &skills.Skill{
		Frontmatter: frontmatter,
		Content:     content,
		Resources:   resources,
		Scripts:     scripts,
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

	for _, kv := range yamlKeyValueRegex.FindAllStringSubmatch(yamlContent, -1) {
		value := kv[2]
		if value == "" {
			value = kv[3]
		}
		switch strings.ToLower(kv[1]) {
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

func (s *Source) discoverResourceFiles(skillFS fs.FS, skillName string) []skills.Resource {
	seen := make(map[string]bool)
	var resources []skills.Resource
	for _, directory := range s.resourceDirectories {
		for _, fileName := range s.walkConfiguredDirectory(skillFS, directory, skillName, s.allowedResourceExtensions, "resource") {
			key := strings.ToLower(fileName)
			if seen[key] {
				continue
			}
			seen[key] = true
			resources = append(resources, skills.Resource{
				Name: fileName,
				Read: func(context.Context) (any, error) {
					data, err := fs.ReadFile(skillFS, fileName)
					if err != nil {
						return nil, err
					}
					return string(data), nil
				},
			})
		}
	}
	return resources
}

func (s *Source) discoverScriptFiles(skillFS fs.FS, skillName string) []skills.Script {
	seen := make(map[string]bool)
	scripts := make([]skills.Script, 0)
	for _, directory := range s.scriptDirectories {
		for _, fileName := range s.walkConfiguredDirectory(skillFS, directory, skillName, s.allowedScriptExtensions, "script") {
			key := strings.ToLower(fileName)
			if seen[key] {
				continue
			}
			seen[key] = true
			scripts = append(scripts, newScript(fileName, skillFS, s.scriptRunner))
		}
	}
	return scripts
}

func (s *Source) walkConfiguredDirectory(skillFS fs.FS, directory, skillName string, allowedExtensions map[string]bool, kind string) []string {
	fsDir := directory
	if fsDir == rootDirectoryIndicator {
		fsDir = "."
	}

	if _, err := fs.Stat(skillFS, fsDir); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Warn("Failed to read configured skill directory", "skillName", skillName, "directory", fsDir, "kind", kind, "error", err)
		}
		return nil
	}

	entries, err := fs.ReadDir(skillFS, fsDir)
	if err != nil {
		s.logger.Warn("Failed while scanning configured skill directory", "skillName", skillName, "directory", fsDir, "kind", kind, "error", err)
		return nil
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		normalized := normalizePath(path.Join(fsDir, entry.Name()))
		if strings.EqualFold(path.Base(normalized), skillFileName) {
			continue
		}

		ext := strings.ToLower(path.Ext(normalized))
		if ext == "" || !allowedExtensions[ext] {
			s.logger.Debug("Skipping file: extension not in the allowed list", "skillName", skillName, "filePath", normalized, "extension", ext, "kind", kind)
			continue
		}

		files = append(files, normalized)
	}
	return files
}

func validateAndNormalizeDirectoryNames(directories []string, defaults []string, logger *slog.Logger) []string {
	if directories == nil {
		return append([]string(nil), defaults...)
	}

	normalized := make([]string, 0, len(directories))
	seen := make(map[string]bool, len(directories))
	for _, directory := range directories {
		if strings.TrimSpace(directory) == "" {
			panic("directory names must not be empty or whitespace")
		}
		if directory == rootDirectoryIndicator {
			if !seen[directory] {
				seen[directory] = true
				normalized = append(normalized, directory)
			}
			continue
		}
		if !filepath.IsLocal(directory) {
			logger.Warn("Skipping invalid directory name: must be a relative path with no '..' segments", "directoryName", directory)
			continue
		}
		norm := normalizePath(directory)
		if !seen[norm] {
			seen[norm] = true
			normalized = append(normalized, norm)
		}
	}
	return normalized
}

func normalizePath(value string) string {
	return path.Clean(filepath.ToSlash(value))
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
		Name: name,
		Run: func(ctx context.Context, owner *skills.Skill, arguments map[string]any) (any, error) {
			if _, err := FSFromSkill(owner); err != nil {
				return nil, fmt.Errorf("file-based script %q requires a skill with a backing fs.FS: %w", name, err)
			}
			if runner == nil {
				return nil, fmt.Errorf("script %q cannot be executed because no file script runner was provided", name)
			}
			if arguments == nil {
				arguments = map[string]any{}
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
