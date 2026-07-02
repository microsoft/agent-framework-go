// Copyright (c) Microsoft. All rights reserved.

package demo

import (
	"cmp"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	DefaultDeployment = "gpt-4o-mini"
)

var (
	Endpoint   = strings.TrimSpace(os.Getenv("AZURE_OPENAI_ENDPOINT"))
	Deployment = cmp.Or(strings.TrimSpace(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")), DefaultDeployment)
)

func AzureTokenCredential() *azidentity.DefaultAzureCredential {
	if os.Getenv("AZURE_OPENAI_ENDPOINT") == "" {
		Panic("AZURE_OPENAI_ENDPOINT environment variable is not set.")
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
