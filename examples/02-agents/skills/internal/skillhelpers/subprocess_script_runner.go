// Copyright (c) Microsoft. All rights reserved.

package skillhelpers

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/microsoft/agent-framework-go/agent/skills"
)

const rootFSPropertyKey = "fsskills.rootFS"

// RunSubprocessScript materializes a file-backed skill to a temp directory and executes a script from it.
//
// The args slice contains positional CLI-style string tokens exactly as sent by the LLM
// (for example ["--value", "26.2", "--factor", "1.60934"]). They are passed verbatim
// as subprocess arguments.
func RunSubprocessScript(ctx context.Context, skill *skills.Skill, script *skills.Script, args []string) (any, error) {
	tempDir, err := os.MkdirTemp("", "agent-framework-go-skill-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	if err := materializeSkill(skill, tempDir); err != nil {
		return nil, err
	}

	scriptPath := filepath.Join(tempDir, filepath.FromSlash(script.Name))
	command, scriptArgs, err := scriptCommand(scriptPath, args)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, command, scriptArgs...)
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

func scriptCommand(scriptPath string, args []string) (string, []string, error) {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	switch ext {
	case ".py":
		return "python", append([]string{scriptPath}, args...), nil
	case ".js":
		return "node", append([]string{scriptPath}, args...), nil
	case ".sh":
		return "bash", append([]string{scriptPath}, args...), nil
	case ".ps1":
		return "pwsh", append([]string{scriptPath}, args...), nil
	default:
		return "", nil, fmt.Errorf("unsupported script type %q", ext)
	}
}
