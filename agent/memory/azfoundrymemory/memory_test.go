// Copyright (c) Microsoft. All rights reserved.

package azfoundrymemory

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

func TestNewProviderPanicsWithEmptyEndpoint(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(" ", validCredential, "memory", validScope, Config{})
}

func TestNewProviderPanicsWithNilCredential(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(validEndpoint, nil, "memory", validScope, Config{})
}

func TestNewProviderPanicsWithEmptyMemoryStoreName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(validEndpoint, validCredential, " ", validScope, Config{})
}

func TestNewProviderPanicsWithNilScope(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(validEndpoint, validCredential, "memory", nil, Config{})
}

func TestNewProviderSucceedsWithValidParameters(t *testing.T) {
	provider := NewProvider(validEndpoint, validCredential, "memory", validScope, Config{})
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if provider.ContextProvider() == nil {
		t.Fatal("context provider is nil")
	}
	if got := provider.scope(nil); got != "user-456" {
		t.Fatalf("scope = %q, want user-456", got)
	}
}

func TestNewProviderDefaultsMatchFoundryMemoryOptions(t *testing.T) {
	provider := NewProvider(validEndpoint, validCredential, "memory", validScope, Config{})
	if provider.config.ContextPrompt != defaultMemoryContextPrompt {
		t.Fatalf("ContextPrompt = %q", provider.config.ContextPrompt)
	}
	if provider.config.MaxMemories != defaultMaxMemories {
		t.Fatalf("MaxMemories = %d, want %d", provider.config.MaxMemories, defaultMaxMemories)
	}
	if provider.config.UpdateDelay != 0 {
		t.Fatalf("UpdateDelay = %d, want immediate", provider.config.UpdateDelay)
	}
	if provider.ContextProvider().StoreRequestFilter == nil {
		t.Fatal("StoreRequestFilter was not defaulted")
	}
	if provider.config.SearchInputFilter == nil {
		t.Fatal("SearchInputFilter was not defaulted")
	}
	if provider.ContextProvider().SourceID != defaultSourceID {
		t.Fatalf("SourceID = %q, want %q", provider.ContextProvider().SourceID, defaultSourceID)
	}
}

func TestNewProviderUsesCustomSearchInputFilter(t *testing.T) {
	called := false
	filter := func(context.Context, []*message.Message) ([]*message.Message, error) {
		called = true
		return nil, nil
	}
	provider := NewProvider(validEndpoint, validCredential, "memory", validScope, Config{SearchInputFilter: filter})

	_, _, err := provider.provide(context.Background(), []*message.Message{message.NewText("hello")})
	if err != nil {
		t.Fatalf("provide error = %v", err)
	}
	if !called {
		t.Fatal("custom search input filter was not called")
	}
}

func TestScopePanicsWhenEmpty(t *testing.T) {
	provider := NewProvider(validEndpoint, validCredential, "memory", func(*agent.Session) string { return " " }, Config{})
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
	provider := NewProvider(validEndpoint, validCredential, "memory", validScope, Config{ClientOptions: clientOptions, Logger: logger})

	err := provider.store(context.Background(), []*message.Message{message.NewText("remember me")}, nil)
	if err != nil {
		t.Fatalf("store error = %v", err)
	}

	logText := logs.String()
	if !strings.Contains(logText, "azfoundrymemory: failed to update memories") || !strings.Contains(logText, expected.Error()) {
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
