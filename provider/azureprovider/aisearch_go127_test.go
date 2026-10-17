// Copyright (c) Microsoft. All rights reserved.

//go:build go1.27

package azureprovider_test

import (
	"net/http"
	"testing"

	"github.com/microsoft/agent-framework-go/provider/azureprovider"
)

func TestAISearch_GenericCollectionMethod(t *testing.T) {
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"id":"one","text":"saved"}`), nil
	}, azureprovider.AISearchConfig{})
	collection, err := store.Collection[string, document]("records", nil)
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := collection.Get(t.Context(), "one", nil)
	if err != nil || !found || value.Text != "saved" {
		t.Fatalf("generic method retrieval = (%v, %v, %v)", value, found, err)
	}
}
