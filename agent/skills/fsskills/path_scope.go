// Copyright (c) Microsoft. All rights reserved.

package fsskills

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

type skillPathScope struct {
	rootFS         fs.FS
	skillDirPath   string
	skillDirPrefix string
}

func newSkillPathScope(rootFS fs.FS, skillDirPath string) skillPathScope {
	skillDirPath = path.Clean(skillDirPath)
	skillDirPrefix := ""
	if skillDirPath != "." {
		skillDirPrefix = skillDirPath + "/"
	}
	return skillPathScope{
		rootFS:         rootFS,
		skillDirPath:   skillDirPath,
		skillDirPrefix: skillDirPrefix,
	}
}

func (s skillPathScope) validateDiscoveredPathForUse(relativePath, kind string) (string, error) {
	fullPath := path.Clean(relativePath)
	if s.skillDirPath != "." {
		fullPath = path.Clean(path.Join(s.skillDirPath, relativePath))
	}
	if !s.contains(fullPath) {
		return "", fmt.Errorf("%s file %q references a path outside the skill directory", kind, relativePath)
	}
	if hasLinkOrInspectionFailureInPath(s.rootFS, fullPath) {
		return "", fmt.Errorf("%s file %q has a symbolic link or inspection failure in its path; symbolic links are not allowed", kind, relativePath)
	}
	if _, err := fs.Stat(s.rootFS, fullPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s file %q was not found in the skill directory", kind, relativePath)
		}
		return "", err
	}
	return fullPath, nil
}

func (s skillPathScope) contains(fullPath string) bool {
	if s.skillDirPath == "." {
		return fullPath != ".." && !strings.HasPrefix(fullPath, "../")
	}
	return fullPath == s.skillDirPath || strings.HasPrefix(fullPath, s.skillDirPrefix)
}

func hasLinkOrInspectionFailureInPath(filesystem fs.FS, pathToCheck string) bool {
	currentPath := ""
	for _, segment := range strings.Split(path.Clean(pathToCheck), "/") {
		if segment == "" || segment == "." {
			continue
		}
		if currentPath == "" {
			currentPath = segment
		} else {
			currentPath = path.Join(currentPath, segment)
		}
		if isUnsafePath(filesystem, currentPath) {
			return true
		}
	}
	return false
}

func isUnsafePath(filesystem fs.FS, filePath string) bool {
	readLinkFS, ok := filesystem.(fs.ReadLinkFS)
	if !ok {
		return true
	}

	info, err := readLinkFS.Lstat(filePath)
	if err == nil {
		return info.Mode()&fs.ModeSymlink != 0
	}
	return !errors.Is(err, fs.ErrNotExist)
}
