// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

type conversionMockContent struct {
	message.Content
	header message.ContentHeader
}

func (*conversionMockContent) MarshalJSON() ([]byte, error) {
	return nil, errors.New("unsupported content must not be serialized")
}

func (c *conversionMockContent) Header() *message.ContentHeader { return &c.header }

func TestContentsToPartsIgnoresUnsupported(t *testing.T) {
	contents := []message.Content{
		&message.TextContent{Text: "First text"},
		&conversionMockContent{},
		&message.URIContent{URI: "https://example.com/file.txt", MediaType: "file/txt"},
		&conversionMockContent{},
		&message.TextContent{Text: "Second text"},
	}
	parts, err := contentsToParts(contents, nil)
	if err != nil {
		t.Fatal(err)
	}
	if parts == nil || len(parts) != 3 {
		t.Fatalf("parts count = %d, want 3", len(parts))
	}
	if text, ok := parts[0].Content.(a2a.Text); !ok || text != "First text" {
		t.Errorf("first part = %#v, want text First text", parts[0].Content)
	}
	if url, ok := parts[1].Content.(a2a.URL); !ok || string(url) != "https://example.com/file.txt" {
		t.Errorf("second part = %#v, want URL https://example.com/file.txt", parts[1].Content)
	}
	if text, ok := parts[2].Content.(a2a.Text); !ok || text != "Second text" {
		t.Errorf("third part = %#v, want text Second text", parts[2].Content)
	}
}

func TestContentsToPartsEmpty(t *testing.T) {
	contents := make([]message.Content, 0)
	parts, err := contentsToParts(contents, nil)
	if err != nil {
		t.Fatal(err)
	}
	if parts != nil {
		t.Fatalf("parts = %v, want nil", parts)
	}
}

func TestContentsToPartsMultiple(t *testing.T) {
	contents := []message.Content{
		&message.TextContent{Text: "First text"},
		&message.URIContent{URI: "https://example.com/file1.txt", MediaType: "file/txt"},
		&message.TextContent{Text: "Second text"},
	}
	parts, err := contentsToParts(contents, nil)
	if err != nil {
		t.Fatal(err)
	}
	if parts == nil || len(parts) != 3 {
		t.Fatalf("parts count = %d, want 3", len(parts))
	}
	if text, ok := parts[0].Content.(a2a.Text); !ok || text != "First text" {
		t.Errorf("first part = %#v, want text First text", parts[0].Content)
	}
	if url, ok := parts[1].Content.(a2a.URL); !ok || string(url) != "https://example.com/file1.txt" {
		t.Errorf("second part = %#v, want URL https://example.com/file1.txt", parts[1].Content)
	}
	if text, ok := parts[2].Content.(a2a.Text); !ok || text != "Second text" {
		t.Errorf("third part = %#v, want text Second text", parts[2].Content)
	}
}

func TestArtifactPartsToContentsMultiple(t *testing.T) {
	artifact := &a2a.Artifact{
		Parts: a2a.ContentParts{
			a2a.NewTextPart("Part 1"),
			a2a.NewTextPart("Part 2"),
			a2a.NewTextPart("Part 3"),
		},
	}
	contents, err := partsToContents(artifact.Parts, make(message.Contents, 0))
	if err != nil {
		t.Fatal(err)
	}
	if contents == nil || len(contents) != 3 {
		t.Fatalf("contents count = %d, want 3", len(contents))
	}
	for i, want := range []string{"Part 1", "Part 2", "Part 3"} {
		text, ok := contents[i].(*message.TextContent)
		if !ok {
			t.Fatalf("contents[%d] type = %T, want TextContent", i, contents[i])
		}
		if text.Text != want {
			t.Errorf("contents[%d] text = %q, want %q", i, text.Text, want)
		}
	}
}

func TestArtifactPartsToContentsEmpty(t *testing.T) {
	artifact := &a2a.Artifact{
		Parts: make(a2a.ContentParts, 0),
	}
	contents, err := partsToContents(artifact.Parts, make(message.Contents, 0))
	if err != nil {
		t.Fatal(err)
	}
	if contents == nil || len(contents) != 0 {
		t.Fatalf("contents = %v, want non-nil empty contents", contents)
	}
}

func TestYieldTaskUnsplitArtifactBeforeInput(t *testing.T) {
	task := &a2a.Task{
		ID:        "task1",
		Artifacts: []*a2a.Artifact{{Parts: a2a.ContentParts{a2a.NewTextPart("partial result")}}},
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateInputRequired,
			Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart("Need more info")}},
		},
	}
	var updates []*agent.ResponseUpdate
	if !yieldTask(func(update *agent.ResponseUpdate, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
		return true
	}, task, false) {
		t.Fatal("yieldTask stopped")
	}
	if len(updates) != 1 || len(updates[0].Contents) != 2 {
		t.Fatalf("updates = %#v, want one update with two contents", updates)
	}
	for i, want := range []string{"partial result", "Need more info"} {
		text, ok := updates[0].Contents[i].(*message.TextContent)
		if !ok || text.Text != want {
			t.Errorf("contents[%d] = %#v, want text %q", i, updates[0].Contents[i], want)
		}
	}
}

func TestTaskToMessagesNilTask(t *testing.T) {
	var task *a2a.Task
	if _, err := taskToMessages(task); err == nil {
		t.Fatal("want error for nil task")
	}
}

func TestTaskToContentsNilTask(t *testing.T) {
	var task *a2a.Task
	if _, err := taskToContents(task); err == nil {
		t.Fatal("want error for nil task")
	}
}

func TestTaskToMessagesEmptyArtifacts(t *testing.T) {
	task := &a2a.Task{ID: "task1", Artifacts: make([]*a2a.Artifact, 0), Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	result, err := taskToMessages(task)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("messages = %#v, want nil", result)
	}
}

func TestTaskToMessagesNilArtifacts(t *testing.T) {
	task := &a2a.Task{ID: "task1", Artifacts: nil, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	result, err := taskToMessages(task)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("messages = %#v, want nil", result)
	}
}

func TestTaskToContentsEmptyArtifacts(t *testing.T) {
	task := &a2a.Task{ID: "task1", Artifacts: make([]*a2a.Artifact, 0), Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	result, err := taskToContents(task)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("contents = %#v, want nil", result)
	}
}

func TestTaskToContentsNilArtifacts(t *testing.T) {
	task := &a2a.Task{ID: "task1", Artifacts: nil, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	result, err := taskToContents(task)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("contents = %#v, want nil", result)
	}
}

func TestTaskToMessagesArtifact(t *testing.T) {
	artifact := &a2a.Artifact{Parts: a2a.ContentParts{a2a.NewTextPart("response")}}
	task := &a2a.Task{ID: "task1", Artifacts: []*a2a.Artifact{artifact}, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	result, err := taskToMessages(task)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatal("want non-nil, nonempty messages")
	}
	for _, msg := range result {
		if msg.Role != message.RoleAssistant {
			t.Errorf("role = %q, want assistant", msg.Role)
		}
	}
	if len(result[0].Contents) == 0 || fmt.Sprint(result[0].Contents[0]) != "response" {
		t.Fatalf("contents = %#v, want response", result[0].Contents)
	}
}

func TestTaskToContentsMultipleArtifacts(t *testing.T) {
	artifact1 := &a2a.Artifact{Parts: a2a.ContentParts{a2a.NewTextPart("content1")}}
	artifact2 := &a2a.Artifact{Parts: a2a.ContentParts{a2a.NewTextPart("content2"), a2a.NewTextPart("content3")}}
	task := &a2a.Task{ID: "task1", Artifacts: []*a2a.Artifact{artifact1, artifact2}, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	result, err := taskToContents(task)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result) != 3 {
		t.Fatalf("contents = %#v, want three", result)
	}
	for i, want := range []string{"content1", "content2", "content3"} {
		if fmt.Sprint(result[i]) != want {
			t.Errorf("contents[%d] = %q, want %q", i, fmt.Sprint(result[i]), want)
		}
	}
}

func TestTaskToMessagesInputRequired(t *testing.T) {
	task := &a2a.Task{
		ID: "task1", Artifacts: nil,
		Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart("What is your destination?")}}},
	}
	result, err := taskToMessages(task)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result) != 1 {
		t.Fatalf("messages = %#v, want one", result)
	}
	if result[0].Role != message.RoleAssistant {
		t.Errorf("role = %q, want assistant", result[0].Role)
	}
	var texts []*message.TextContent
	for _, content := range result[0].Contents {
		if text, ok := content.(*message.TextContent); ok {
			texts = append(texts, text)
		}
	}
	if len(texts) != 1 || texts[0].Text != "What is your destination?" {
		t.Fatalf("texts = %#v, want destination question", texts)
	}
}

func TestTaskToContentsInputRequired(t *testing.T) {
	task := &a2a.Task{
		ID: "task1", Artifacts: nil,
		Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart("What is your destination?")}}},
	}
	result, err := taskToContents(task)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("want non-nil contents")
	}
	var texts []*message.TextContent
	for _, content := range result {
		if text, ok := content.(*message.TextContent); ok {
			texts = append(texts, text)
		}
	}
	if len(texts) != 1 || texts[0].Text != "What is your destination?" {
		t.Fatalf("texts = %#v, want destination question", texts)
	}
}

func TestTaskToMessagesArtifactsAndInputRequired(t *testing.T) {
	task := &a2a.Task{
		ID:        "task1",
		Artifacts: []*a2a.Artifact{{Parts: a2a.ContentParts{a2a.NewTextPart("partial result")}}},
		Status:    a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart("Need more info")}}},
	}
	result, err := taskToMessages(task)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result) != 2 {
		t.Fatalf("messages = %#v, want two", result)
	}
	if result[0].String() != "partial result" {
		t.Errorf("first message = %q, want partial result", result[0].String())
	}
	var texts []*message.TextContent
	for _, content := range result[1].Contents {
		if text, ok := content.(*message.TextContent); ok {
			texts = append(texts, text)
		}
	}
	if len(texts) != 1 {
		t.Fatalf("second message texts = %#v, want one", texts)
	}
}

func TestArtifactToMessagePartsMetadataAndRaw(t *testing.T) {
	artifact := &a2a.Artifact{
		ID: "artifact-comprehensive", Name: "comprehensive-artifact",
		Parts:    a2a.ContentParts{a2a.NewTextPart("First part"), a2a.NewTextPart("Second part"), a2a.NewTextPart("Third part")},
		Metadata: map[string]any{"key1": "value1", "key2": 42},
	}
	result, err := artifactToMessage(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("want non-nil message")
	}
	if result.Role != message.RoleAssistant {
		t.Errorf("role = %q, want assistant", result.Role)
	}
	if len(result.Contents) != 3 {
		t.Fatalf("contents count = %d, want three", len(result.Contents))
	}
	for i, want := range []string{"First part", "Second part", "Third part"} {
		text, ok := result.Contents[i].(*message.TextContent)
		if !ok || text.Text != want {
			t.Errorf("contents[%d] = %#v, want text %q", i, result.Contents[i], want)
		}
	}
	if result.AdditionalProperties == nil || len(result.AdditionalProperties) != 2 {
		t.Fatalf("metadata = %#v, want two properties", result.AdditionalProperties)
	}
	for _, key := range []string{"key1", "key2"} {
		if _, ok := result.AdditionalProperties[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
	if result.RawRepresentation == nil || result.RawRepresentation != artifact {
		t.Errorf("raw representation = %#v, want artifact", result.RawRepresentation)
	}
}

func TestStatusInputContentsNilMessage(t *testing.T) {
	status := &a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: nil}
	result, err := statusInputContents(status)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("contents = %#v, want nil", result)
	}
}

func TestStatusInputContentsOtherState(t *testing.T) {
	status := &a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart("Some text")}}}
	result, err := statusInputContents(status)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("contents = %#v, want nil", result)
	}
}

func TestStatusInputContentsMultipleRequests(t *testing.T) {
	status := &a2a.TaskStatus{
		State:   a2a.TaskStateInputRequired,
		Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart("First request"), a2a.NewTextPart("Second request"), a2a.NewTextPart("Third request")}},
	}
	result, err := statusInputContents(status)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result) != 3 {
		t.Fatalf("contents = %#v, want three", result)
	}
	for i, want := range []string{"First request", "Second request", "Third request"} {
		text, ok := result[i].(*message.TextContent)
		if !ok || text.Text != want {
			t.Errorf("contents[%d] = %#v, want text %q", i, result[i], want)
		}
	}
}

func TestStatusInputContentsRawAndMetadata(t *testing.T) {
	part := a2a.NewTextPart("Input request")
	part.Metadata = map[string]any{"key1": "value1", "key2": "value2"}
	status := &a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: &a2a.Message{Parts: a2a.ContentParts{part}}}
	result, err := statusInputContents(status)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatal("want non-nil, nonempty contents")
	}
	content, ok := result[0].(*message.TextContent)
	if !ok {
		t.Fatalf("content = %T, want TextContent", result[0])
	}
	if content.RawRepresentation != part {
		t.Errorf("raw representation = %#v, want part", content.RawRepresentation)
	}
	if content.AdditionalProperties == nil {
		t.Fatal("want non-nil metadata")
	}
	for _, key := range []string{"key1", "key2"} {
		if _, ok := content.AdditionalProperties[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
}

func TestStatusInputContentsEmptyParts(t *testing.T) {
	status := &a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: &a2a.Message{Parts: make(a2a.ContentParts, 0)}}
	result, err := statusInputContents(status)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("contents = %#v, want nil", result)
	}
}

func TestMessagesToA2AMessageMultipleContents(t *testing.T) {
	messages := []*message.Message{{Role: message.RoleUser, Contents: message.Contents{
		&message.URIContent{URI: "https://example.com/report.pdf", MediaType: "file/pdf"},
		&message.TextContent{Text: "please summarize the file content"},
		&message.TextContent{Text: "and send it to me over email"},
	}}}
	result, err := messagesToA2AMessage(nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	assertConvertedCollectionMessage(t, result)
}

func TestMessagesToA2AMessageMixedMessages(t *testing.T) {
	messages := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.URIContent{URI: "https://example.com/report.pdf", MediaType: "file/pdf"}}},
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "please summarize the file content"}}},
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "and send it to me over email"}}},
	}
	result, err := messagesToA2AMessage(nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	assertConvertedCollectionMessage(t, result)
}

func assertConvertedCollectionMessage(t *testing.T, result *a2a.Message) {
	t.Helper()
	if result == nil {
		t.Fatal("converted message is nil")
	}
	if result.ID == "" {
		t.Error("message ID is empty")
	}
	if result.Role != a2a.MessageRoleUser {
		t.Errorf("role = %q, want user", result.Role)
	}
	if result.Parts == nil || len(result.Parts) != 3 {
		t.Fatalf("parts count = %d, want 3", len(result.Parts))
	}
	if url, ok := result.Parts[0].Content.(a2a.URL); !ok || string(url) != "https://example.com/report.pdf" {
		t.Errorf("first part = %#v, want report URL", result.Parts[0].Content)
	}
	for i, want := range []string{"please summarize the file content", "and send it to me over email"} {
		if text, ok := result.Parts[i+1].Content.(a2a.Text); !ok || string(text) != want {
			t.Errorf("part %d = %#v, want text %q", i+1, result.Parts[i+1].Content, want)
		}
	}
}

// Unused transport operations are not part of these single-send fixtures.
type inputRequiredTransport struct {
	a2aclient.Transport
	request *a2a.SendMessageRequest
	sends   int
	streams int
}

func (m *inputRequiredTransport) SendMessage(_ context.Context, _ a2aclient.ServiceParams, request *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	m.request = request
	m.sends++
	return inputRequiredResponse(), nil
}

func (m *inputRequiredTransport) SendStreamingMessage(_ context.Context, _ a2aclient.ServiceParams, request *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	m.request = request
	m.streams++
	return func(yield func(a2a.Event, error) bool) {
		yield(inputRequiredResponse(), nil)
	}
}

func inputRequiredResponse() *a2a.Message {
	return &a2a.Message{ID: "response-456", Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("Booking confirmed")}}
}

func (*inputRequiredTransport) Destroy() error { return nil }

func TestRunWithPreconfiguredInputRequiredTask(t *testing.T) {
	assertPreconfiguredInputRequiredTask(t, false)
}

func TestRunStreamingWithPreconfiguredInputRequiredTask(t *testing.T) {
	assertPreconfiguredInputRequiredTask(t, true)
}

func assertPreconfiguredInputRequiredTask(t *testing.T, stream bool) {
	t.Helper()
	transport := &inputRequiredTransport{}
	client, err := a2aclient.NewFromEndpoints(t.Context(), []*a2a.AgentInterface{
		a2a.NewAgentInterface("test://localhost", a2a.TransportProtocol("test")),
	}, a2aclient.WithDefaultsDisabled(), a2aclient.WithTransport("test", a2aclient.TransportFactoryFn(func(context.Context, *a2a.AgentCard, *a2a.AgentInterface) (a2aclient.Transport, error) {
		return transport, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Destroy(); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	})
	a := NewAgent(client, AgentConfig{})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	setTaskID(session, "task-123")
	setLastTaskState(session, a2a.TaskStateInputRequired)
	input := &message.Message{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "New York to London"}}}
	response := a.RunMessage(t.Context(), input, agent.WithSession(session), agent.Stream(stream))
	if stream {
		for _, err := range response {
			if err != nil {
				t.Fatal(err)
			}
		}
	} else if _, err := response.Collect(); err != nil {
		t.Fatal(err)
	}
	if transport.request == nil || transport.request.Message == nil {
		t.Fatal("no message was sent")
	}
	if got := transport.request.Message.TaskID; got != "task-123" {
		t.Errorf("task ID = %q, want task-123", got)
	}
	if transport.request.Message.ReferenceTasks != nil {
		t.Errorf("reference tasks = %v, want nil", transport.request.Message.ReferenceTasks)
	}
	if transport.sends+transport.streams != 1 || (stream && transport.streams != 1) || (!stream && transport.sends != 1) {
		t.Errorf("send counts = %d non-streaming, %d streaming, want one matching send", transport.sends, transport.streams)
	}
}
