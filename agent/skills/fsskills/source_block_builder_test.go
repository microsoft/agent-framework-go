// Copyright (c) Microsoft. All rights reserved.

package fsskills

import (
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/skills"
)

func TestBuildAvailableResourcesBlock_WithDescription_EmitsDescriptionAttribute(t *testing.T) {
	resources := []skills.Resource{
		{Name: "docs/guide.md", Description: "The user guide"},
	}
	got := buildAvailableResourcesBlock(resources)
	if !strings.Contains(got, `<resource name="docs/guide.md" description="The user guide"/>`) {
		t.Fatalf("expected description attribute in resource element, got: %s", got)
	}
}

func TestBuildAvailableResourcesBlock_WithoutDescription_OmitsDescriptionAttribute(t *testing.T) {
	resources := []skills.Resource{
		{Name: "docs/guide.md"},
	}
	got := buildAvailableResourcesBlock(resources)
	if !strings.Contains(got, `<resource name="docs/guide.md"/>`) {
		t.Fatalf("expected self-closing resource without description attribute, got: %s", got)
	}
	if strings.Contains(got, "description=") {
		t.Fatalf("expected no description attribute when description is empty, got: %s", got)
	}
}

func TestBuildAvailableResourcesBlock_DescriptionIsXmlEscaped(t *testing.T) {
	resources := []skills.Resource{
		{Name: "data.xml", Description: `A "quoted" & <tagged> resource`},
	}
	got := buildAvailableResourcesBlock(resources)
	if strings.Contains(got, `"A "quoted"`) {
		t.Fatalf("expected description to be XML-escaped, got: %s", got)
	}
	if !strings.Contains(got, `description="A &quot;quoted&quot; &amp; &lt;tagged&gt; resource"`) {
		t.Fatalf("expected XML-escaped description attribute, got: %s", got)
	}
}

func TestBuildAvailableScriptsBlock_WithDescription_NoSchema_EmitsDescriptionAttribute(t *testing.T) {
	scripts := []skills.Script{
		{Name: "scripts/run.py", Description: "Runs the pipeline"},
	}
	got := buildAvailableScriptsBlock(scripts)
	if !strings.Contains(got, `<script name="scripts/run.py" description="Runs the pipeline"/>`) {
		t.Fatalf("expected description attribute in self-closing script element, got: %s", got)
	}
}

func TestBuildAvailableScriptsBlock_WithDescription_WithSchema_EmitsDescriptionAttribute(t *testing.T) {
	scripts := []skills.Script{
		{Name: "scripts/run.py", Description: "Runs the pipeline", ParametersSchema: `{"type":"object"}`},
	}
	got := buildAvailableScriptsBlock(scripts)
	if !strings.Contains(got, `<script name="scripts/run.py" description="Runs the pipeline">`) {
		t.Fatalf("expected description attribute in expanded script element, got: %s", got)
	}
	if !strings.Contains(got, `<parameters_schema>{"type":"object"}</parameters_schema>`) {
		t.Fatalf("expected parameters_schema element, got: %s", got)
	}
}

func TestBuildAvailableScriptsBlock_WithoutDescription_OmitsDescriptionAttribute(t *testing.T) {
	scripts := []skills.Script{
		{Name: "scripts/run.py"},
	}
	got := buildAvailableScriptsBlock(scripts)
	if !strings.Contains(got, `<script name="scripts/run.py"/>`) {
		t.Fatalf("expected self-closing script without description attribute, got: %s", got)
	}
	if strings.Contains(got, "description=") {
		t.Fatalf("expected no description attribute when description is empty, got: %s", got)
	}
}

func TestBuildAvailableScriptsBlock_DescriptionIsXmlEscaped(t *testing.T) {
	scripts := []skills.Script{
		{Name: "run.py", Description: `Convert "mph" to km/h`},
	}
	got := buildAvailableScriptsBlock(scripts)
	if strings.Contains(got, `description="Convert "mph"`) {
		t.Fatalf("expected description to be XML-escaped, got: %s", got)
	}
	if !strings.Contains(got, `description="Convert &quot;mph&quot; to km/h"`) {
		t.Fatalf("expected XML-escaped description attribute, got: %s", got)
	}
}
