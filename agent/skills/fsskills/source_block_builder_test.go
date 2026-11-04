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

	t.Run("special characters", func(t *testing.T) {
		scripts := []skills.Script{
			{Name: "deploy", Description: `has <special> & "chars"`},
		}
		got := buildAvailableScriptsBlock(scripts)
		if !strings.Contains(got, `description="has &lt;special&gt; &amp; &quot;chars&quot;"`) {
			t.Fatalf("expected XML-escaped description attribute, got: %s", got)
		}
	})
}

func TestBuildAvailableResourcesBlock_EmptyList_ReturnsSelfClosingElement(t *testing.T) {
	if got := buildAvailableResourcesBlock([]skills.Resource{}); got != "\n<available_resources />" {
		t.Fatalf("block = %q, want self-closing resources element", got)
	}
}

func TestBuildAvailableResourcesBlock_WithResources_EmitsOptionalDescriptions(t *testing.T) {
	got := buildAvailableResourcesBlock([]skills.Resource{
		{Name: "config", Description: "A described resource."},
		{Name: "table"},
	})
	for _, want := range []string{
		"<available_resources>",
		`<resource name="config" description="A described resource."/>`,
		`<resource name="table"/>`,
		"</available_resources>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("block = %q, want %q", got, want)
		}
	}
}

func TestBuildAvailableResourcesBlock_WhitespaceDescription_OmitsDescriptionAttribute(t *testing.T) {
	got := buildAvailableResourcesBlock([]skills.Resource{{Name: "config", Description: "   "}})
	if !strings.Contains(got, `<resource name="config"/>`) || strings.Contains(got, "description=") {
		t.Fatalf("whitespace description must be omitted, got %q", got)
	}
}

func TestBuildAvailableResourcesBlock_NameIsXmlEscaped(t *testing.T) {
	got := buildAvailableResourcesBlock([]skills.Resource{{Name: `a<b>&"c`}})
	if !strings.Contains(got, `<resource name="a&lt;b&gt;&amp;&quot;c"/>`) {
		t.Fatalf("resource name is not escaped: %q", got)
	}
}

func TestBuildAvailableScriptsBlock_EmptyList_ReturnsSelfClosingElement(t *testing.T) {
	if got := buildAvailableScriptsBlock([]skills.Script{}); got != "\n<available_scripts />" {
		t.Fatalf("block = %q, want self-closing scripts element", got)
	}
}

func TestBuildAvailableScriptsBlock_WithoutSchema_UsesSelfClosingScript(t *testing.T) {
	got := buildAvailableScriptsBlock([]skills.Script{{Name: "no-params"}})
	if !strings.Contains(got, `<script name="no-params"/>`) || strings.Contains(got, "<parameters_schema>") {
		t.Fatalf("schema-less script must be self-closing, got %q", got)
	}
}

func TestBuildAvailableScriptsBlock_WhitespaceDescription_OmitsDescriptionAttribute(t *testing.T) {
	got := buildAvailableScriptsBlock([]skills.Script{{Name: "deploy", Description: "   "}})
	if !strings.Contains(got, `<script name="deploy"/>`) || strings.Contains(got, "description=") {
		t.Fatalf("whitespace description must be omitted, got %q", got)
	}
}

func TestBuildAvailableScriptsBlock_WithSchema_PreservesJsonInElement(t *testing.T) {
	got := buildAvailableScriptsBlock([]skills.Script{{
		Name:             "search",
		ParametersSchema: `{"type":"object","properties":{"query":{"type":"string"}}}`,
	}})
	for _, want := range []string{
		`<script name="search">`,
		"<parameters_schema>",
		`"query"`,
		"</parameters_schema>",
		"</script>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("block = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "<![CDATA[") {
		t.Fatalf("schema must not use CDATA: %q", got)
	}
}

func TestBuildAvailableScriptsBlock_NameIsXmlEscaped(t *testing.T) {
	got := buildAvailableScriptsBlock([]skills.Script{{Name: `a<b>&"c`}})
	if !strings.Contains(got, `<script name="a&lt;b&gt;&amp;&quot;c"/>`) {
		t.Fatalf("script name is not escaped: %q", got)
	}
}
