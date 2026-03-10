// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"strings"
)

const (
	skillFileName        = "SKILL.md"
	maxSearchDepth       = 2
	maxNameLength        = 64
	maxDescriptionLength = 1024
)

// Matches YAML frontmatter delimited by "---" lines.
var frontmatterRegex = regexp.MustCompile(`(?ms)\A^---\s*$(.+?)^---\s*$`)

// Matches markdown links to local resource files.
// Supports optional ./ or ../ prefixes; excludes URLs (no ":" in the path character class).
var resourceLinkRegex = regexp.MustCompile(`\[.*?\]\((\.?\.?/?[\w][\w\-./]*\.\w+)\)`)

// Matches valid skill names: lowercase letters, numbers, and single hyphens,
// not starting or ending with a hyphen, and not containing consecutive hyphens.
var validNameRegex = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Matches YAML "key: value" lines. Group 1 = key, Group 2 = quoted value, Group 3 = unquoted value.
var yamlKeyValueRegex = regexp.MustCompile(`(?m)^\s*(\w+)\s*:\s*(?:["'](.+?)["']|(.+?))\s*$`)

type skillLoader struct {
	logger *slog.Logger
}

// discoveredSkillDir represents a discovered skill directory with its sub-filesystem.
type discoveredSkillDir struct {
	fsys fs.FS
	path string // relative path within the parent FS, for logging
}

// discoverAndLoadSkills discovers skill directories and loads valid skills from them.
func (l *skillLoader) discoverAndLoadSkills(filesystems []fs.FS) map[string]*fileAgentSkill {
	skills := make(map[string]*fileAgentSkill)

	discoveredDirs := discoverSkillDirectories(filesystems)
	l.logger.Info("Discovered potential skills", "count", len(discoveredDirs))

	for _, dir := range discoveredDirs {
		skill := l.parseSkillFile(dir.fsys, dir.path)
		if skill == nil {
			continue
		}
		if existing, ok := skills[strings.ToLower(skill.frontmatter.Name)]; ok {
			l.logger.Warn("Duplicate skill name: skill skipped in favor of existing skill",
				"skillName", skill.frontmatter.Name,
				"newPath", dir.path,
				"existingPath", existing.sourcePath,
			)
			continue
		}
		skills[strings.ToLower(skill.frontmatter.Name)] = skill
		l.logger.Info("Loaded skill", "skillName", skill.frontmatter.Name)
	}
	l.logger.Info("Successfully loaded skills", "count", len(skills))
	return skills
}

// readSkillResource reads a resource file from the skill's filesystem.
// Path traversal is prevented by the [fs.FS] contract, which rejects paths containing "..".
func (l *skillLoader) readSkillResource(skill *fileAgentSkill, resourceName string) (string, error) {
	resourceName = normalizeResourcePath(resourceName)

	found := false
	for _, r := range skill.resourceNames {
		if strings.EqualFold(r, resourceName) {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("resource '%s' not found in skill '%s'", resourceName, skill.frontmatter.Name)
	}

	l.logger.Info("Reading resource from skill", "fileName", resourceName, "skillName", skill.frontmatter.Name)

	data, err := fs.ReadFile(skill.fs, resourceName)
	if err != nil {
		return "", fmt.Errorf("resource file '%s' is not accessible in skill '%s': %w", resourceName, skill.frontmatter.Name, err)
	}
	return string(data), nil
}

// discoverSkillDirectories searches the given filesystems for directories containing SKILL.md files.
func discoverSkillDirectories(filesystems []fs.FS) []discoveredSkillDir {
	var results []discoveredSkillDir
	for _, fsys := range filesystems {
		searchForSkills(fsys, ".", &results, 0)
	}
	return results
}

func searchForSkills(fsys fs.FS, dir string, results *[]discoveredSkillDir, currentDepth int) {
	skillPath := path.Join(dir, skillFileName)
	if _, err := fs.Stat(fsys, skillPath); err == nil {
		sub, err := fs.Sub(fsys, dir)
		if err == nil {
			*results = append(*results, discoveredSkillDir{fsys: sub, path: dir})
		}
	}
	if currentDepth >= maxSearchDepth {
		return
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			searchForSkills(fsys, path.Join(dir, entry.Name()), results, currentDepth+1)
		}
	}
}

func (l *skillLoader) parseSkillFile(skillFS fs.FS, logPath string) *fileAgentSkill {
	data, err := fs.ReadFile(skillFS, skillFileName)
	if err != nil {
		l.logger.Error("Failed to read SKILL.md", "path", logPath, "error", err)
		return nil
	}
	content := string(data)

	fm, body, ok := l.tryParseSkillDocument(content, logPath)
	if !ok {
		return nil
	}

	resourceNames := extractResourcePaths(body)
	if !l.validateResources(skillFS, resourceNames, fm.Name) {
		return nil
	}

	return &fileAgentSkill{
		frontmatter:   *fm,
		body:          body,
		fs:            skillFS,
		sourcePath:    logPath,
		resourceNames: resourceNames,
	}
}

func (l *skillLoader) tryParseSkillDocument(content, skillFilePath string) (*frontmatter, string, bool) {
	// Strip optional UTF-8 BOM.
	content = strings.TrimPrefix(content, "\uFEFF")

	match := frontmatterRegex.FindStringSubmatchIndex(content)
	if match == nil {
		l.logger.Error("SKILL.md does not contain valid YAML frontmatter delimited by '---'",
			"skillFilePath", skillFilePath)
		return nil, "", false
	}

	yamlContent := strings.TrimSpace(content[match[2]:match[3]])

	var name, description string

	for _, kvMatch := range yamlKeyValueRegex.FindAllStringSubmatch(yamlContent, -1) {
		key := kvMatch[1]
		value := kvMatch[2] // quoted value
		if value == "" {
			value = kvMatch[3] // unquoted value
		}

		switch strings.ToLower(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}

	if strings.TrimSpace(name) == "" {
		l.logger.Error("SKILL.md is missing a 'name' field in frontmatter",
			"skillFilePath", skillFilePath)
		return nil, "", false
	}

	if len(name) > maxNameLength || !validNameRegex.MatchString(name) {
		l.logger.Error("SKILL.md has an invalid 'name' value",
			"skillFilePath", skillFilePath,
			"reason", fmt.Sprintf("Must be %d characters or fewer, using only lowercase letters, numbers, and single hyphens, and must not start or end with a hyphen or contain consecutive hyphens.", maxNameLength))
		return nil, "", false
	}

	// Enforce that the frontmatter name matches the parent skill directory name when discoverable.
	if skillFilePath != "." {
		dirName := path.Base(skillFilePath)
		if name != dirName {
			l.logger.Error("SKILL.md name does not match skill directory name",
				"skillFilePath", skillFilePath,
				"name", name,
				"directoryName", dirName)
			return nil, "", false
		}
	}

	if strings.TrimSpace(description) == "" {
		l.logger.Error("SKILL.md is missing a 'description' field in frontmatter",
			"skillFilePath", skillFilePath)
		return nil, "", false
	}

	if len(description) > maxDescriptionLength {
		l.logger.Error("SKILL.md has an invalid 'description' value",
			"skillFilePath", skillFilePath,
			"reason", fmt.Sprintf("Must be %d characters or fewer.", maxDescriptionLength))
		return nil, "", false
	}

	// Body is the content after the closing --- delimiter.
	body := strings.TrimSpace(content[match[1]:])

	return &frontmatter{Name: name, Description: description}, body, true
}

// validateResources checks that all referenced resources exist and are accessible
// within the skill's filesystem.
func (l *skillLoader) validateResources(skillFS fs.FS, resourceNames []string, skillName string) bool {
	for _, resourceName := range resourceNames {
		if _, err := fs.Stat(skillFS, resourceName); err != nil {
			l.logger.Warn("Excluding skill: resource is not accessible",
				"skillName", skillName,
				"resourceName", resourceName,
				"error", err)
			return false
		}
	}
	return true
}

// extractResourcePaths extracts relative resource file paths from markdown link syntax.
func extractResourcePaths(content string) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, match := range resourceLinkRegex.FindAllStringSubmatch(content, -1) {
		path := normalizeResourcePath(match[1])
		lower := strings.ToLower(path)
		if !seen[lower] {
			seen[lower] = true
			paths = append(paths, path)
		}
	}
	return paths
}

// normalizeResourcePath normalizes a relative resource path by trimming a leading "./" prefix
// and replacing backslashes with forward slashes.
func normalizeResourcePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	return path
}
