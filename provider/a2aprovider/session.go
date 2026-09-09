// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/agent"
)

const (
	taskIDStateKey = "a2aprovider.taskID"
	taskStateKey   = "a2aprovider.taskState"
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
	if session == nil {
		return
	}
	if taskID == "" {
		session.Delete(taskIDStateKey)
		return
	}
	session.Set(taskIDStateKey, taskID)
}

func getTaskID(session *agent.Session) string {
	var taskID string
	if ok, err := session.Get(taskIDStateKey, &taskID); err != nil || !ok {
		return ""
	}
	return taskID
}

// TaskIDFromSession returns the current A2A task ID stored in session state.
func TaskIDFromSession(session *agent.Session) string {
	return getTaskID(session)
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
