// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"slices"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/agent"
)

const (
	taskIDsStateKey = "a2aprovider.taskIDs"
	taskStateKey    = "a2aprovider.taskState"
)

func setContextID(session *agent.Session, contextID string) {
	if contextID == "" {
		return
	}
	session.SetServiceID(contextID)
}

func getContextID(session *agent.Session) string {
	return session.ServiceID()
}

func setTaskID(session *agent.Session, taskID string) {
	if taskID == "" {
		return
	}
	existing := getTaskIDs(session)
	// The same task ID is reported on every streamed event for a task, so guard
	// against re-adding an ID that is already stored. slices.Contains keeps
	// interleaved distinct task IDs working.
	if slices.Contains(existing, taskID) {
		return
	}
	setTaskIDs(session, append(existing, taskID))
}

func setTaskIDs(session *agent.Session, taskIDs []string) {
	if len(taskIDs) == 0 {
		return
	}
	session.Set(taskIDsStateKey, taskIDs)
}

func getTaskIDs(session *agent.Session) []string {
	var taskIDs []string
	if ok, err := session.Get(taskIDsStateKey, &taskIDs); err != nil || !ok {
		return nil
	}
	return taskIDs
}

// TaskIDsFromSession returns all known A2A task IDs stored in session state.
func TaskIDsFromSession(session *agent.Session) []string {
	return getTaskIDs(session)
}

func setLastTaskState(session *agent.Session, state a2a.TaskState) {
	if session == nil {
		return
	}
	if state == a2a.TaskStateUnspecified {
		session.Delete(taskStateKey)
		return
	}
	session.Set(taskStateKey, string(state))
}

func getLastTaskState(session *agent.Session) a2a.TaskState {
	if session == nil {
		return a2a.TaskStateUnspecified
	}
	var state string
	if ok, err := session.Get(taskStateKey, &state); err != nil || !ok {
		return a2a.TaskStateUnspecified
	}
	return a2a.TaskState(state)
}
