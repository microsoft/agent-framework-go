// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
)

func TestAISearch_RejectsCrossOriginRedirects(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	for _, useCustomClient := range []bool{false, true} {
		config := azureprovider.AISearchConfig{
			APIKey: azcore.NewKeyCredential("must-not-leak"),
			ClientOptions: azcore.ClientOptions{
				InsecureAllowCredentialWithHTTP: true,
			},
		}
		config.ClientOptions.Retry.MaxRetries = -1
		client := &http.Client{}
		if useCustomClient {
			config.ClientOptions.Transport = client
		}
		search, err := azureprovider.NewAISearch(source.URL, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := search.CollectionExists(t.Context(), "records"); err == nil || !strings.Contains(err.Error(), "redirects") {
			t.Fatalf("cross-origin redirect = %v", err)
		}
		if destinationCalls.Load() != 0 || client.CheckRedirect != nil {
			t.Fatal("redirect exposed credentials or modified the borrowed HTTP client")
		}
	}
}

func TestAISearch_PreservesCustomRedirectPolicy(t *testing.T) {
	var policyCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	wantErr := errors.New("custom redirect policy")
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		policyCalls.Add(1)
		return wantErr
	}}
	config := azureprovider.AISearchConfig{
		APIKey: azcore.NewKeyCredential("key"),
		ClientOptions: azcore.ClientOptions{
			Transport: httpClient, InsecureAllowCredentialWithHTTP: true,
		},
	}
	config.ClientOptions.Retry.MaxRetries = -1
	search, err := azureprovider.NewAISearch(server.URL, config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = search.CollectionExists(t.Context(), "records")
	if !errors.Is(err, wantErr) || policyCalls.Load() != 1 {
		t.Fatalf("custom redirect policy = %v, calls=%d", err, policyCalls.Load())
	}
}

func TestAISearch_HTTPRequiresExplicitOptIn(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	search, err := azureprovider.NewAISearch(server.URL, azureprovider.AISearchConfig{APIKey: azcore.NewKeyCredential("key")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := search.CollectionExists(t.Context(), "records"); err == nil {
		t.Fatal("credentials were sent over HTTP without opting in")
	}
	if calls.Load() != 0 {
		t.Fatal("insecure request reached the server")
	}
}

func TestAISearch_CancelInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	}, azureprovider.AISearchConfig{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := store.CollectionExists(ctx, "records")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation = %v", err)
	}
}

func TestAISearch_DeletionAndListFailures(t *testing.T) {
	missing := newStore(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
	}, azureprovider.AISearchConfig{})
	if err := missing.EnsureCollectionDeleted(t.Context(), "records"); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{}`, `{"value":[{}]}`, `{"value":[],"@odata.nextLink":"https://other.example/indexes"}`,
	} {
		calls := 0
		store := newStore(t, func(*http.Request) (*http.Response, error) {
			calls++
			return response(http.StatusOK, payload), nil
		}, azureprovider.AISearchConfig{})
		_ = sequenceError(t, store.ListCollectionNames(t.Context()))
		if calls != 1 {
			t.Fatalf("invalid index response triggered %d calls", calls)
		}
	}
}
