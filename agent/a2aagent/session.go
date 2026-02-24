// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import "github.com/microsoft/agent-framework-go/agent/memory"

const (
	taskIDStateKey = "a2aagent.taskID"
)

func setContextID(session *memory.Session, contextID string) {
	if session != nil {
		session.ServiceID = contextID
	}
}

func getContextID(session *memory.Session) string {
	if session == nil {
		return ""
	}
	return session.ServiceID
}

func setTaskID(session *memory.Session, taskID string) {
	if session != nil {
		session.Set(taskIDStateKey, taskID)
	}
}

func getTaskID(session *memory.Session) string {
	if session == nil {
		return ""
	}
	var taskID string
	_, _ = session.Get(taskIDStateKey, &taskID)
	return taskID
}

// TaskIDFromSession returns the A2A task ID stored in session state.
func TaskIDFromSession(session *memory.Session) string {
	return getTaskID(session)
}
