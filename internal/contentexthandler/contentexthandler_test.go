// Copyright (c) Microsoft. All rights reserved.

package contentexthandler

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestMakePendingRequestsStateKey(t *testing.T) {
	got := makePendingRequestsStateKey("toolCalls")
	const want = "toolCalls_PendingRequests"
	if got != want {
		t.Fatalf("makePendingRequestsStateKey() = %q, want %q", got, want)
	}
}

func TestCreateExternalRequestID(t *testing.T) {
	got := createExternalRequestID("approval-port", "request-1")
	const want = "13:approval-port:request-1"
	if got != want {
		t.Fatalf("createExternalRequestID() = %q, want %q", got, want)
	}
}

func TestHandlerDispatchRequestUsesExternalRequestID(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "call-port",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	handler := New(Options[string, string]{
		Port:      port,
		RequestID: func(request string) string { return request },
	})

	var posted *workflow.ExternalRequest
	ctx := &workflow.Context{
		PostRequest: func(request *workflow.ExternalRequest) error {
			posted = request
			return nil
		},
	}

	if err := handler.DispatchRequest(ctx, "call-1"); err != nil {
		t.Fatalf("DispatchRequest() error = %v", err)
	}
	if posted == nil {
		t.Fatal("DispatchRequest() did not post a request")
	}
	const wantID = "9:call-port:call-1"
	if posted.RequestID != wantID {
		t.Fatalf("posted RequestID = %q, want %q", posted.RequestID, wantID)
	}
}
