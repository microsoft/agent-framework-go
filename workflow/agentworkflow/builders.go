// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/workflow"
)

func validateBuilderAgents(builderName string, agents []*agent.Agent) error {
	if len(agents) == 0 {
		return fmt.Errorf("agentworkflow: %s requires at least one agent", builderName)
	}
	for index, currentAgent := range agents {
		if currentAgent == nil {
			return fmt.Errorf("agentworkflow: %s agent at index %d is nil", builderName, index)
		}
	}
	return nil
}

type outputDesignations map[*agent.Agent]map[workflow.OutputTag]struct{}

func (d outputDesignations) explicit() bool {
	return d != nil
}

func (d outputDesignations) withOutputFrom(agents ...*agent.Agent) (outputDesignations, error) {
	if d == nil {
		d = make(outputDesignations)
	}
	for index, currentAgent := range agents {
		if currentAgent == nil {
			return d, fmt.Errorf("agentworkflow: output agent at index %d is nil", index)
		}
		if _, ok := d[currentAgent]; !ok {
			d[currentAgent] = make(map[workflow.OutputTag]struct{})
		}
	}
	return d, nil
}

func (d outputDesignations) withIntermediateOutputFrom(agents ...*agent.Agent) (outputDesignations, error) {
	d, err := d.withOutputFrom(agents...)
	if err != nil {
		return d, err
	}
	for _, currentAgent := range agents {
		d[currentAgent][workflow.OutputTagIntermediate] = struct{}{}
	}
	return d, nil
}

func applyOutputDesignations(
	bld *workflow.Builder,
	designations outputDesignations,
	bindingsByAgent map[*agent.Agent]workflow.ExecutorBinding,
	orchestrationKind string,
	applyDefaults func(),
) (*workflow.Builder, error) {
	if !designations.explicit() {
		applyDefaults()
		return bld, nil
	}
	for currentAgent, tags := range designations {
		binding, ok := bindingsByAgent[currentAgent]
		if !ok {
			return bld, fmt.Errorf("agentworkflow: output designation references agent %q, which is not a participant in this %s workflow", agentNameForError(currentAgent), orchestrationKind)
		}
		if len(tags) == 0 {
			bld = bld.WithOutputFrom(binding)
			continue
		}
		bld = bld.WithIntermediateOutputFrom(binding)
	}
	return bld, nil
}

func agentNameForError(a *agent.Agent) string {
	if a == nil {
		return ""
	}
	if name := a.Name(); name != "" {
		return name
	}
	return a.ID()
}

func applyBuilderMetadata(bld *workflow.Builder, name string, description string) *workflow.Builder {
	if strings.TrimSpace(name) != "" {
		bld = bld.WithName(name)
	}
	if strings.TrimSpace(description) != "" {
		bld = bld.WithDescription(description)
	}
	return bld
}
