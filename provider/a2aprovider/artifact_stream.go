// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"cmp"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/agent"
)

type artifactStreamWriter struct {
	infoProvider      a2a.TaskInfoProvider
	usedArtifactIDs   map[a2a.ArtifactID]struct{}
	currentMessageID  string
	currentArtifactID a2a.ArtifactID
	bufferedUpdate    *agent.ResponseUpdate
	shouldAppend      bool
}

func newArtifactStreamWriter(infoProvider a2a.TaskInfoProvider) *artifactStreamWriter {
	return &artifactStreamWriter{
		infoProvider:    infoProvider,
		usedArtifactIDs: make(map[a2a.ArtifactID]struct{}),
	}
}

func (w *artifactStreamWriter) Write(update *agent.ResponseUpdate) ([]*a2a.TaskArtifactUpdateEvent, error) {
	if update == nil {
		return nil, nil
	}

	events := make([]*a2a.TaskArtifactUpdateEvent, 0, 2)
	if w.currentArtifactID == "" {
		w.startArtifact(update.MessageID, update.ResponseID)
	} else if w.isNewMessage(update.MessageID) {
		evt, err := w.flushBuffered(true)
		if err != nil {
			return events, err
		}
		if evt != nil {
			events = append(events, evt)
		}
		w.startArtifact(update.MessageID, update.ResponseID)
	}

	parts, err := contentsToParts(update.Contents, nil)
	if err != nil {
		if flushEvt, flushErr := w.flushBuffered(true); flushErr == nil && flushEvt != nil {
			events = append(events, flushEvt)
		}
		return events, err
	}
	if len(parts) == 0 {
		return events, nil
	}

	evt, err := w.flushBuffered(false)
	if err != nil {
		return events, err
	}
	if evt != nil {
		events = append(events, evt)
	}
	w.bufferedUpdate = update
	return events, nil
}

func (w *artifactStreamWriter) Complete() (*a2a.TaskArtifactUpdateEvent, error) {
	return w.flushBuffered(true)
}

func (w *artifactStreamWriter) flushBuffered(lastChunk bool) (*a2a.TaskArtifactUpdateEvent, error) {
	if w.bufferedUpdate == nil {
		return nil, nil
	}

	evt, err := responseUpdateToArtifactEventWithOptions(
		w.infoProvider,
		w.currentArtifactID,
		w.shouldAppend,
		lastChunk,
		w.bufferedUpdate,
	)
	if err != nil {
		return nil, err
	}

	w.bufferedUpdate = nil
	w.shouldAppend = true
	return evt, nil
}

func (w *artifactStreamWriter) isNewMessage(messageID string) bool {
	return messageID != "" && messageID != w.currentMessageID
}

func (w *artifactStreamWriter) startArtifact(messageID, responseID string) {
	w.currentMessageID = messageID

	idSource := cmp.Or(messageID, responseID)
	if idSource == "" {
		idSource = string(a2a.NewArtifactID())
	}

	artifactID := a2a.ArtifactID(idSource)
	if _, used := w.usedArtifactIDs[artifactID]; !used {
		w.currentArtifactID = artifactID
		w.usedArtifactIDs[artifactID] = struct{}{}
	} else {
		w.currentArtifactID = a2a.NewArtifactID()
		w.usedArtifactIDs[w.currentArtifactID] = struct{}{}
	}
	w.shouldAppend = false
}
