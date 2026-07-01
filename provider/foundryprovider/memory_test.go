// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/azaiprojects"
	"github.com/microsoft/agent-framework-go/message"
)

const validEndpoint = "https://example.test"

var validCredential azcore.TokenCredential = &azfake.TokenCredential{}

func TestNewMemoryProviderPanicsWithEmptyEndpoint(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewMemoryProvider(" ", validCredential, "memory", validScope, MemoryProviderConfig{})
}

func TestNewMemoryProviderPanicsWithNilCredential(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewMemoryProvider(validEndpoint, nil, "memory", validScope, MemoryProviderConfig{})
}

func TestNewMemoryProviderPanicsWithEmptyMemoryStoreName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewMemoryProvider(validEndpoint, validCredential, " ", validScope, MemoryProviderConfig{})
}

func TestNewMemoryProviderPanicsWithNilScope(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewMemoryProvider(validEndpoint, validCredential, "memory", nil, MemoryProviderConfig{})
}

func TestNewMemoryProviderSucceedsWithValidParameters(t *testing.T) {
	provider := NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, MemoryProviderConfig{})
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if got := provider.scope(nil); got != "user-456" {
		t.Fatalf("scope = %q, want user-456", got)
	}
}

func TestNewMemoryProviderDefaultsMatchFoundryMemoryOptions(t *testing.T) {
	provider := NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, MemoryProviderConfig{})
	if provider.config.ContextPrompt != defaultMemoryContextPrompt {
		t.Fatalf("ContextPrompt = %q", provider.config.ContextPrompt)
	}
	if provider.config.MaxMemories != defaultMaxMemories {
		t.Fatalf("MaxMemories = %d, want %d", provider.config.MaxMemories, defaultMaxMemories)
	}
	if provider.config.UpdateDelay != 0 {
		t.Fatalf("UpdateDelay = %d, want immediate", provider.config.UpdateDelay)
	}
	if provider.config.SearchInputFilter == nil {
		t.Fatal("SearchInputFilter was not defaulted")
	}
	if provider.providerConfig.StoreInputRequestMessageFilter == nil {
		t.Fatal("StoreInputRequestMessageFilter was not defaulted")
	}
	if provider.providerConfig.SourceID != defaultSourceID {
		t.Fatalf("SourceID = %q, want %q", provider.providerConfig.SourceID, defaultSourceID)
	}
}

func TestNewMemoryProviderUsesCustomSearchInputFilter(t *testing.T) {
	called := false
	filter := func(context.Context, []*message.Message) ([]*message.Message, error) {
		called = true
		return nil, nil
	}
	provider := NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, MemoryProviderConfig{SearchInputFilter: filter})

	_, _, err := provider.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{message.NewText("hello")}})
	if err != nil {
		t.Fatalf("provide error = %v", err)
	}
	if !called {
		t.Fatal("custom search input filter was not called")
	}
}

func TestScopePanicsWhenEmpty(t *testing.T) {
	provider := NewMemoryProvider(validEndpoint, validCredential, "memory", func(*agent.Session) string { return " " }, MemoryProviderConfig{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = provider.scope(nil)
}

func TestStoreLogsUpdateFailureAndDoesNotReturnError(t *testing.T) {
	expected := errors.New("update failed")
	clientOptions := azcore.ClientOptions{Transport: failingTransport{err: expected}}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	provider := NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, MemoryProviderConfig{ClientOptions: clientOptions, Logger: logger})

	err := provider.store(t.Context(), agent.InvokedContext{RequestMessages: []*message.Message{message.NewText("remember me")}})
	if err != nil {
		t.Fatalf("store error = %v", err)
	}

	logText := logs.String()
	if !strings.Contains(logText, "foundrymemory: failed to update memories") || !strings.Contains(logText, expected.Error()) {
		t.Fatalf("logs = %q", logText)
	}
	if strings.Contains(logText, "user-456") {
		t.Fatalf("logs should not include scope: %q", logText)
	}
}

func TestMemoryItemsAndContents(t *testing.T) {
	items := updateMemoryItems([]*message.Message{
		message.NewText("remember me"),
		{Role: message.RoleTool, Contents: message.Contents{&message.TextContent{Text: "skip tool"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "assistant text"}}},
	})
	if len(items) != 2 {
		t.Fatalf("items length = %d, want 2: %#v", len(items), items)
	}
	if items[0]["role"] != "user" || items[1]["role"] != "assistant" {
		t.Fatalf("items = %#v", items)
	}

	content := "memory one"
	contents := memoryContents([]azaiprojects.MemorySearchItem{{MemoryItem: &azaiprojects.MemoryItem{Content: &content}}})
	if len(contents) != 1 || contents[0] != content {
		t.Fatalf("contents = %#v", contents)
	}
}

func validScope(*agent.Session) string { return "user-456" }

type failingTransport struct {
	err error
}

func (transport failingTransport) Do(*http.Request) (*http.Response, error) {
	return nil, transport.err
}
