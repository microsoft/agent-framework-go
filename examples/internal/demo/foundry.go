// Copyright (c) Microsoft. All rights reserved.

package demo

import (
	"cmp"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

var (
	FoundryProjectEndpoint = strings.TrimSpace(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"))
	FoundryModel           = cmp.Or(strings.TrimSpace(os.Getenv("FOUNDRY_MODEL")), DefaultDeployment)
	FoundryEmbeddingModel  = cmp.Or(os.Getenv("FOUNDRY_EMBEDDING_MODEL"), "text-embedding-3-large")
)

func FoundryTokenCredential() *azidentity.DefaultAzureCredential {
	if FoundryProjectEndpoint == "" {
		Panic("FOUNDRY_PROJECT_ENDPOINT environment variable is not set.")
	}
	// azidentity.NewDefaultAzureCredential is convenient for development but requires careful consideration in production.
	// In production, consider using a specific credential, such as `azidentity.NewManagedIdentityCredential`,
	// to avoid latency issues, unintended credential probing, and potential security risks from fallback mechanisms.
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		Panicf("failed to create Azure default credential: %v", err)
	}
	return token
}
