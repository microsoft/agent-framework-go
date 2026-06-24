// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
)

type outputFilter struct {
	tagsByExecutor map[string][]workflow.OutputTag
}

func newOutputFilter(wf *workflow.Workflow) *outputFilter {
	if wf == nil {
		return &outputFilter{}
	}
	return &outputFilter{tagsByExecutor: wf.OutputExecutors()}
}

func (f *outputFilter) tryGetTags(executorID string) ([]workflow.OutputTag, bool) {
	if f == nil {
		return nil, false
	}
	tags, ok := f.tagsByExecutor[executorID]
	if !ok {
		return nil, false
	}
	return slices.Clone(tags), true
}
