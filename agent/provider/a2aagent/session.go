// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import "github.com/microsoft/agent-framework-go/agent"

const (
	taskIDsStateKey = "a2aagent.taskIDs"
)

func setContextID(session agent.Session, contextID string) {
	session.SetServiceID(contextID)
}

func getContextID(session agent.Session) string {
	return session.ServiceID()
}

func setTaskID(session agent.Session, taskID string) {
	if taskID == "" {
		return
	}
	setTaskIDs(session, append(getTaskIDs(session), taskID))
}

func setTaskIDs(session agent.Session, taskIDs []string) {
	if len(taskIDs) == 0 {
		return
	}
	session.Set(taskIDsStateKey, taskIDs)
}

func getTaskIDs(session agent.Session) []string {
	var taskIDs []string
	if ok, err := session.Get(taskIDsStateKey, &taskIDs); err != nil || !ok {
		return nil
	}
	return taskIDs
}

// TaskIDsFromSession returns all known A2A task IDs stored in session state.
func TaskIDsFromSession(session agent.Session) []string {
	return getTaskIDs(session)
}
