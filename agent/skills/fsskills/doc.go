// Copyright (c) Microsoft. All rights reserved.

// Package fsskills discovers and loads skills from a filesystem (an fs.FS),
// materializing skill directories, their resources, and file-backed scripts as
// skills.Skill values.
//
// Loaded skill content preserves SKILL.md and appends resource and script
// catalogs. Catalog entries omit descriptions that are empty or only whitespace.
//
// Frontmatter uses lightweight scalar parsing rather than a full YAML decoder.
// Empty quoted values retain their quote tokens. Metadata values are read as
// single-line scalars; block-scalar indicators in metadata remain literal.
package fsskills
