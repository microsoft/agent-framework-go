// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/tool"
)

type skillScriptTool struct {
	provider *ContextProvider
	skills   providedSkillSet
	schema   *jsonschema.Schema
	resolved *jsonschema.Resolved
}

func newSkillScriptTool(provider *ContextProvider, skills providedSkillSet) tool.FuncTool {
	schema, err := jsonschema.For[struct {
		SkillName  string `json:"skillName" jsonschema:"The name of the skill"`
		ScriptName string `json:"scriptName" jsonschema:"The exact script name to run"`
		Arguments  any    `json:"arguments,omitempty" jsonschema:"JSON arguments in the format described by the script"`
	}](nil)
	if err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return &skillScriptTool{provider: provider, skills: skills, schema: schema, resolved: resolved}
}

func (*skillScriptTool) Name() string { return runSkillScriptToolName }

func (*skillScriptTool) Description() string { return "Runs a script associated with a skill." }

func (t *skillScriptTool) Schema() any { return t.schema }

func (*skillScriptTool) ReturnSchema() any { return &jsonschema.Schema{} }

func (t *skillScriptTool) Call(ctx context.Context, args string) (any, error) {
	if err := t.provider.checkOpen(); err != nil {
		return nil, err
	}
	var input struct {
		SkillName  string          `json:"skillName"`
		ScriptName string          `json:"scriptName"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return nil, fmt.Errorf("skills: decode script call: %w", err)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(args))
	// Validation must not round large integer arguments through float64.
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("skills: decode script arguments: %w", err)
	}
	if err := t.resolved.Validate(value); err != nil {
		return nil, fmt.Errorf("skills: invalid script call: %w", err)
	}
	return t.provider.runSkillScript(ctx, t.skills, input.SkillName, input.ScriptName, input.Arguments)
}
