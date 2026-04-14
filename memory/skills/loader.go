// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	skillFileName        = "SKILL.md"
	maxSearchDepth       = 2
	maxNameLength        = 64
	maxDescriptionLength = 1024

	// "." means the skill directory root itself (no subdirectory descent constraint).
	rootDirectoryIndicator = "."
)

// Standard subdirectory names per https://agentskills.io/specification#directory-structure.
var defaultResourceDirectories = []string{"references", "assets"}

var defaultResourceExtensions = []string{".md", ".json", ".yaml", ".yml", ".csv", ".xml", ".txt"}

// Matches YAML frontmatter delimited by "---" lines.
var frontmatterRegex = regexp.MustCompile(`(?ms)\A^---\s*$(.+?)^---\s*$`)

// Matches valid skill names: lowercase letters, numbers, and single hyphens,
// not starting or ending with a hyphen, and not containing consecutive hyphens.
var validNameRegex = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Matches YAML "key: value" lines. Group 1 = key, Group 2 = quoted value, Group 3 = unquoted value.
var yamlKeyValueRegex = regexp.MustCompile(`(?m)^\s*(\w+)\s*:\s*(?:["'](.+?)["']|(.+?))\s*$`)

type skillLoader struct {
	logger                    *slog.Logger
	resourceDirectories       []string
	allowedResourceExtensions map[string]bool
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
	resourceName = normalizePath(resourceName)

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

	resourceNames := l.discoverResourceFiles(skillFS, fm.Name)

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

// discoverResourceFiles scans configured resource directories within a skill's filesystem
// for resource files matching the configured extensions.
//
// By default, scans "references/" and "assets/" subdirectories as specified by the
// Agent Skills specification (https://agentskills.io/specification).
// Configure [Config.ResourceDirectories] to scan different or additional directories,
// including "." for the skill root itself.
// Each directory is scanned non-recursively (only direct children).
func (l *skillLoader) discoverResourceFiles(skillFS fs.FS, skillName string) []string {
	seen := make(map[string]bool)
	var resources []string

	for _, dir := range l.resourceDirectories {
		isRoot := dir == rootDirectoryIndicator
		fsDir := dir
		if isRoot {
			fsDir = "."
		}

		entries, err := fs.ReadDir(skillFS, fsDir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				l.logger.Warn("Failed to read resource directory",
					"skillName", skillName,
					"directory", fsDir,
					"error", err)
			}
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			fileName := entry.Name()

			// Exclude SKILL.md itself.
			if strings.EqualFold(fileName, skillFileName) {
				continue
			}

			// Filter by extension.
			ext := strings.ToLower(path.Ext(fileName))
			if ext == "" || !l.allowedResourceExtensions[ext] {
				l.logger.Debug("Skipping file: extension not in the allowed list",
					"skillName", skillName,
					"filePath", path.Join(dir, fileName),
					"extension", ext)
				continue
			}

			resourcePath := path.Join(fsDir, fileName)
			lower := strings.ToLower(resourcePath)
			if !seen[lower] {
				seen[lower] = true
				resources = append(resources, resourcePath)
			}
		}
	}

	return resources
}

// validateAndNormalizeDirectoryNames validates and normalizes directory names.
// Empty/whitespace names cause a panic (programming error). Absolute paths and
// paths containing ".." segments are silently skipped with a warning.
//
// To additionally prevent symlink escapes out of the directory tree,
// callers should prefer [os.Root.FS] over [os.DirFS] when constructing the
// fs.FS passed to [New].
func validateAndNormalizeDirectoryNames(directories []string, defaults []string, logger *slog.Logger) []string {
	if directories == nil {
		return defaults
	}

	var normalized []string
	seen := make(map[string]bool)

	for _, dir := range directories {
		if strings.TrimSpace(dir) == "" {
			panic("directory names must not be empty or whitespace")
		}

		// "." is valid — it means the skill root directory.
		if dir == rootDirectoryIndicator {
			if !seen[dir] {
				seen[dir] = true
				normalized = append(normalized, dir)
			}
			continue
		}

		// Reject absolute paths and any path segments that escape upward.
		if !filepath.IsLocal(dir) {
			logger.Warn("Skipping invalid directory name: must be a relative path with no '..' segments",
				"directoryName", dir)
			continue
		}

		norm := normalizePath(dir)
		if !seen[norm] {
			seen[norm] = true
			normalized = append(normalized, norm)
		}
	}

	return normalized
}

// buildExtensionSet creates a set from the given extensions, using defaults if none are provided.
// Each extension must start with ".".
func buildExtensionSet(extensions []string, defaults []string) map[string]bool {
	if extensions == nil {
		extensions = defaults
	}
	validateExtensions(extensions)
	set := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		set[strings.ToLower(ext)] = true
	}
	return set
}

func validateExtensions(extensions []string) {
	for _, ext := range extensions {
		if ext == "" || ext[0] != '.' {
			panic(fmt.Sprintf("invalid extension %q: must start with '.'", ext))
		}
	}
}

// normalizePath normalizes a relative path or directory name using
// [filepath.Clean] and converts to forward slashes.
func normalizePath(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}
