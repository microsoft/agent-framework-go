//go:build windows

// Copyright (c) Microsoft. All rights reserved.

package fsskills_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
)

func TestFileSource_JunctionedSkillDirectory_SkipsLinkedSkill(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside", "evil-skill")
	createSkillDir(t, filepath.Join(base, "outside"), "evil-skill", "Junctioned skill", "Body.")
	createSkillDir(t, root, "good-skill", "A skill", "Body.")
	junction := filepath.Join(root, "evil-skill")
	commandInterpreter := os.Getenv("ComSpec")
	if commandInterpreter == "" {
		commandInterpreter = "cmd.exe"
	}
	if output, err := exec.Command(commandInterpreter, "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("creating directory junction: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil {
			t.Errorf("removing directory junction: %v", err)
		}
	})

	loaded := loadFileSkills(t, fsskills.NewSource(os.DirFS(root)))
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "good-skill" {
		t.Fatalf("Skills() = %#v, want only good-skill", loaded)
	}
}
