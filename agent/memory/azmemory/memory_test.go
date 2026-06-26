// Copyright (c) Microsoft. All rights reserved.

package azmemory

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azaiprojects"
	projectsfake "github.com/Azure/azure-sdk-for-go/sdk/ai/azaiprojects/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func TestNewProviderPanicsWithNilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(nil, "memory", validScope, Config{})
}

func TestNewProviderPanicsWithEmptyMemoryStoreName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(&azaiprojects.MemoryStoresClient{}, " ", validScope, Config{})
}

func TestNewProviderPanicsWithNilScope(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewProvider(&azaiprojects.MemoryStoresClient{}, "memory", nil, Config{})
}

func TestNewProviderSucceedsWithValidParameters(t *testing.T) {
	provider := NewProvider(&azaiprojects.MemoryStoresClient{}, "memory", validScope, Config{})
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
	provider := NewProvider(&azaiprojects.MemoryStoresClient{}, "memory", validScope, Config{})
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
	if provider.ContextProvider().SourceID != defaultSourceID {
		t.Fatalf("SourceID = %q, want %q", provider.ContextProvider().SourceID, defaultSourceID)
	}
}

func TestScopePanicsWhenEmpty(t *testing.T) {
	provider := NewProvider(&azaiprojects.MemoryStoresClient{}, "memory", func(*agent.Session) string { return " " }, Config{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = provider.scope(nil)
}

func TestStoreLogsUpdateFailureAndDoesNotReturnError(t *testing.T) {
	expected := errors.New("update failed")
	client := newMemoryStoresClient(t, &projectsfake.MemoryStoresServer{
		BeginUpdateMemories: func(context.Context, string, string, *azaiprojects.MemoryStoresClientBeginUpdateMemoriesOptions) (azfake.PollerResponder[azaiprojects.MemoryStoresClientUpdateMemoriesResponse], azfake.ErrorResponder) {
			var errResp azfake.ErrorResponder
			errResp.SetError(expected)
			return azfake.PollerResponder[azaiprojects.MemoryStoresClientUpdateMemoriesResponse]{}, errResp
		},
	})

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	provider := NewProvider(client, "memory", validScope, Config{Logger: logger})

	err := provider.store(context.Background(), []*message.Message{message.NewText("remember me")}, nil)
	if err != nil {
		t.Fatalf("store error = %v", err)
	}

	logText := logs.String()
	if !strings.Contains(logText, "azmemory: failed to update memories") || !strings.Contains(logText, expected.Error()) {
		t.Fatalf("logs = %q", logText)
	}
	if strings.Contains(logText, "user-456") {
		t.Fatalf("logs should not include scope: %q", logText)
	}
}

func TestMemoryItemsAndContents(t *testing.T) {
	items := memoryItems([]*message.Message{
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

func newMemoryStoresClient(t *testing.T, srv *projectsfake.MemoryStoresServer) *azaiprojects.MemoryStoresClient {
	t.Helper()
	client, err := azaiprojects.NewClient("https://example.test", &azfake.TokenCredential{}, &azaiprojects.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: projectsfake.NewMemoryStoresServerTransport(srv),
		},
	})
	if err != nil {
		t.Fatalf("NewClient error = %v", err)
	}
	return client.NewMemoryStoresClient()
}
