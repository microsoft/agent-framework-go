// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func jsonBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func newStore(t *testing.T, transport transportFunc, config azureprovider.AISearchConfig) *azureprovider.AISearch {
	t.Helper()
	if config.Credential == nil && config.APIKey == nil {
		config.APIKey = azcore.NewKeyCredential("test-key")
	}
	config.ClientOptions.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("api-version") != "2025-09-01" {
			t.Errorf("API version = %q", request.URL.Query().Get("api-version"))
		}
		result, err := transport(request)
		if result != nil {
			result.Request = request
		}
		return result, err
	})
	config.ClientOptions.Retry.MaxRetries = -1
	store, err := azureprovider.NewAISearch("https://example.search.windows.net", config)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type document struct {
	ID     string    `vectordata:"key,storage=id"`
	Text   string    `vectordata:"data,indexed,fulltext,storage=text"`
	Rank   int32     `vectordata:"data,indexed,storage=rank"`
	Tags   []string  `vectordata:"data,indexed,storage=tags"`
	Vector []float32 `vectordata:"vector,dimensions=3,storage=vector"`
	Other  string
}

func openCollection[K, R any](t *testing.T, store *azureprovider.AISearch, definition *vectordata.Definition) vectordata.Collection[K, R] {
	t.Helper()
	collection, err := vectordata.GetCollection[K, R](store.Store, "records", definition)
	if err != nil {
		t.Fatal(err)
	}
	return collection
}

func collect[T any](t *testing.T, sequence func(func(T, error) bool)) []T {
	t.Helper()
	var values []T
	for value, err := range sequence {
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}

func sequenceError[T any](t *testing.T, sequence func(func(T, error) bool)) error {
	t.Helper()
	var failure error
	for _, err := range sequence {
		if failure != nil {
			t.Fatal("iterator continued after an error")
		}
		failure = err
	}
	if failure == nil {
		t.Fatal("iterator did not return an error")
	}
	return failure
}

type credentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f credentialFunc) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, options)
}

func TestAISearch_AuthenticationAndValidation(t *testing.T) {
	key := azcore.NewKeyCredential("initial")
	for _, audience := range []string{"", "https://search.azure.us"} {
		tokenCalls, requests := 0, 0
		credential := credentialFunc(func(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			tokenCalls++
			want := audience
			if want == "" {
				want = "https://search.azure.com"
			}
			if !slices.Equal(options.Scopes, []string{want + "/.default"}) {
				t.Errorf("token scopes = %v", options.Scopes)
			}
			return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
		})
		store := newStore(t, func(request *http.Request) (*http.Response, error) {
			requests++
			if request.Header.Get("Authorization") != "Bearer token" || request.Header.Get("api-key") != "" {
				t.Errorf("token authentication headers = %v", request.Header)
			}
			return response(http.StatusOK, `{"name":"records"}`), nil
		}, azureprovider.AISearchConfig{Credential: credential, Audience: audience})
		if tokenCalls != 0 || requests != 0 {
			t.Fatal("constructor performed I/O")
		}
		if exists, err := store.CollectionExists(t.Context(), "records"); err != nil || !exists || tokenCalls != 1 || requests != 1 {
			t.Fatalf("CollectionExists = (%v, %v), tokens=%d requests=%d", exists, err, tokenCalls, requests)
		}
	}
	expectedKey := "initial"
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("api-key") != expectedKey || request.Header.Get("Authorization") != "" {
			t.Errorf("API key authentication headers = %v", request.Header)
		}
		return response(http.StatusOK, `{"name":"records"}`), nil
	}, azureprovider.AISearchConfig{APIKey: key})
	for _, value := range []string{"initial", "rotated"} {
		expectedKey = value
		key.Update(value)
		if _, err := store.CollectionExists(t.Context(), "records"); err != nil {
			t.Fatal(err)
		}
	}
	token := credentialFunc(func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, errors.New("unused credential")
	})
	for _, test := range []struct {
		endpoint string
		config   azureprovider.AISearchConfig
	}{
		{"", azureprovider.AISearchConfig{APIKey: key}},
		{"relative", azureprovider.AISearchConfig{APIKey: key}},
		{"https://user:secret@example.test", azureprovider.AISearchConfig{APIKey: key}},
		{"https://example.test/path", azureprovider.AISearchConfig{APIKey: key}},
		{"https://example.test?query=1", azureprovider.AISearchConfig{APIKey: key}},
		{"https://example.test#fragment", azureprovider.AISearchConfig{APIKey: key}},
		{"ftp://example.test", azureprovider.AISearchConfig{APIKey: key}},
		{"https://example.test", azureprovider.AISearchConfig{}},
		{"https://example.test", azureprovider.AISearchConfig{APIKey: key, Credential: token}},
	} {
		if store, err := azureprovider.NewAISearch(test.endpoint, test.config); err == nil || store != nil {
			t.Fatalf("accepted invalid configuration (%q, %#v)", test.endpoint, test.config)
		}
	}
	if _, ok := any(store).(io.Closer); ok {
		t.Fatal("store should not close borrowed resources")
	}
}

func TestAISearch_Lifecycle(t *testing.T) {
	exists, gets, creates, deletes := false, 0, 0, 0
	var schema map[string]any
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			gets++
			if request.URL.Path != "/indexes('records')" {
				t.Errorf("index path = %q", request.URL.Path)
			}
			if !exists {
				return response(http.StatusNotFound, `{"error":{"code":"IndexNotFound","message":"missing"}}`), nil
			}
			return response(http.StatusOK, `{"name":"records"}`), nil
		case http.MethodPost:
			creates++
			if request.URL.Path != "/indexes" {
				t.Errorf("create path = %q", request.URL.Path)
			}
			schema = jsonBody(t, request)
			exists = true
			return response(http.StatusCreated, `{"name":"records"}`), nil
		case http.MethodDelete:
			deletes++
			exists = false
			return response(http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected method %s", request.Method)
			return nil, nil
		}
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, document](t, store, nil)
	if collection.Name() != "records" || gets != 0 || creates != 0 {
		t.Fatal("opening a collection created it")
	}
	for range 2 {
		if err := collection.EnsureExists(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if gets != 2 || creates != 1 || schema["name"] != "records" {
		t.Fatalf("gets=%d creates=%d schema=%v", gets, creates, schema)
	}
	fields := schema["fields"].([]any)
	keyField := fields[0].(map[string]any)
	vectorField := fields[4].(map[string]any)
	if len(fields) != 5 || keyField["name"] != "id" || keyField["key"] != true ||
		keyField["type"] != "Edm.String" || keyField["filterable"] != true || keyField["sortable"] != true ||
		vectorField["name"] != "vector" || vectorField["dimensions"] != float64(3) ||
		vectorField["vectorSearchProfile"] != "vectorProfile" {
		t.Fatalf("index fields = %v", fields)
	}
	vectorConfig := schema["vectorSearch"].(map[string]any)
	algorithm := vectorConfig["algorithms"].([]any)[0].(map[string]any)
	if algorithm["kind"] != "hnsw" || algorithm["name"] != "vectorAlgoConfig" ||
		algorithm["hnswParameters"].(map[string]any)["metric"] != "cosine" {
		t.Fatalf("default vector index = %v", vectorConfig)
	}
	if err := collection.EnsureDeleted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if found, err := collection.Exists(t.Context()); err != nil || found || deletes != 1 {
		t.Fatalf("deleted index exists = (%v, %v), deletes=%d", found, err, deletes)
	}
}

func TestAISearch_ListPaginationAndEarlyStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		calls := 0
		store := newStore(t, func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				if request.URL.Query().Get("$select") != "name" {
					t.Error("listing indexes did not project names")
				}
				return response(http.StatusOK, `{"value":[{"name":"one"}],"@odata.nextLink":"https://example.search.windows.net/indexes?api-version=2025-09-01&$skip=1"}`), nil
			}
			if request.URL.Query().Get("$skip") != "1" {
				t.Error("continuation parameters were lost")
			}
			return response(http.StatusOK, `{"value":[{"name":"two"}]}`), nil
		}, azureprovider.AISearchConfig{})
		sequence := store.ListCollectionNames(t.Context())
		if calls != 0 {
			t.Fatal("listing started eagerly")
		}
		var names []string
		for name, err := range sequence {
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
			if stop {
				break
			}
		}
		want := []string{"one", "two"}
		if stop {
			want = want[:1]
		}
		if !slices.Equal(names, want) || calls != len(want) {
			t.Fatalf("names=%v calls=%d", names, calls)
		}
	}
}

func TestAISearch_ServiceErrorsAndCreateRace(t *testing.T) {
	for _, code := range []string{"ResourceNameAlreadyInUse", "OtherConflict"} {
		store := newStore(t, func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet {
				return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
			}
			return response(http.StatusConflict, `{"error":{"code":"`+code+`","message":"conflict"}}`), nil
		}, azureprovider.AISearchConfig{})
		err := openCollection[string, document](t, store, nil).EnsureExists(t.Context())
		if code == "ResourceNameAlreadyInUse" {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var serviceErr *azcore.ResponseError
			var vectorErr *vectordata.Error
			if !errors.As(err, &serviceErr) || serviceErr.StatusCode != http.StatusConflict ||
				!errors.As(err, &vectorErr) || vectorErr.SystemName != "azure.aisearch" ||
				vectorErr.CollectionName != "records" || vectorErr.OperationName != "CreateIndex" {
				t.Fatalf("service error was not preserved: %v", err)
			}
		}
	}
}

func TestAISearch_CollectionTypes(t *testing.T) {
	calls := 0
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected HTTP request")
	}, azureprovider.AISearchConfig{})
	definition := &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[uuid.UUID]()},
		&vectordata.VectorProperty{Name: "Vector", Type: reflect.TypeFor[[]float32](), Dimensions: 3},
	}}
	for _, recordType := range []func() error{
		func() error {
			_, err := vectordata.GetCollection[string, document](store.Store, "records", nil)
			return err
		},
		func() error {
			_, err := vectordata.GetCollection[string, *document](store.Store, "records", nil)
			return err
		},
		func() error { _, err := store.DynamicCollection("dynamic", definition); return err },
	} {
		if err := recordType(); err != nil {
			t.Fatal(err)
		}
	}
	type unsupported struct {
		ID int `vectordata:"key"`
	}
	if _, err := vectordata.GetCollection[int, unsupported](store.Store, "records", nil); err == nil {
		t.Fatal("accepted integer key")
	}
	if _, err := vectordata.GetCollection[string, document](store.Store, " ", nil); err == nil {
		t.Fatal("accepted blank collection name")
	}
	if _, err := store.DynamicCollection("dynamic", nil); err == nil {
		t.Fatal("accepted dynamic collection without definition")
	}
	if calls != 0 || definition.Properties[0].(*vectordata.KeyProperty).StorageName != "" {
		t.Fatal("collection construction performed I/O or mutated its definition")
	}
}

type trackingBody struct {
	*bytes.Reader
	closes atomic.Int32
}

func (b *trackingBody) Close() error { b.closes.Add(1); return nil }
