// Copyright (c) Microsoft. All rights reserved.

package skillhelpers

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/agent-framework-go/memory/skills"
)

const rootFSPropertyKey = "fsskills.rootFS"

// RunSubprocessScript materializes a file-backed skill to a temp directory and executes a script from it.
func RunSubprocessScript(ctx context.Context, skill *skills.Skill, script *skills.Script, arguments map[string]any) (any, error) {
	tempDir, err := os.MkdirTemp("", "agent-framework-go-skill-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	if err := materializeSkill(skill, tempDir); err != nil {
		return nil, err
	}

	scriptPath := filepath.Join(tempDir, filepath.FromSlash(script.Name))
	command, args, err := scriptCommand(scriptPath, arguments)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = tempDir
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return nil, fmt.Errorf("script execution failed: %s", text)
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func materializeSkill(skill *skills.Skill, destination string) error {
	if skill == nil {
		return fmt.Errorf("skill is required")
	}
	if skill.AdditionalProperties == nil {
		return fmt.Errorf("skill %q does not have a backing fs.FS", skill.Frontmatter.Name)
	}
	root, ok := skill.AdditionalProperties[rootFSPropertyKey].(fs.FS)
	if !ok || root == nil {
		return fmt.Errorf("skill %q does not have a backing fs.FS", skill.Frontmatter.Name)
	}

	return fs.WalkDir(root, ".", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filePath == "." {
			return nil
		}

		targetPath := filepath.Join(destination, filepath.FromSlash(filePath))
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		data, err := fs.ReadFile(root, filePath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, data, 0o644)
	})
}

func scriptCommand(scriptPath string, arguments map[string]any) (string, []string, error) {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	flags := buildCLIFlags(arguments)
	switch ext {
	case ".py":
		return "python", append([]string{scriptPath}, flags...), nil
	case ".js":
		return "node", append([]string{scriptPath}, flags...), nil
	case ".sh":
		return "bash", append([]string{scriptPath}, flags...), nil
	case ".ps1":
		return "pwsh", append([]string{scriptPath}, flags...), nil
	default:
		return "", nil, fmt.Errorf("unsupported script type %q", ext)
	}
}

func buildCLIFlags(arguments map[string]any) []string {
	if len(arguments) == 0 {
		return nil
	}

	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	flags := make([]string, 0, len(arguments)*2)
	for _, key := range keys {
		flag := "--" + strings.TrimLeft(key, "-")
		value := arguments[key]
		if boolean, ok := value.(bool); ok {
			if boolean {
				flags = append(flags, flag)
			}
			continue
		}
		flags = append(flags, flag, fmt.Sprint(value))
	}
	return flags
}
