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
)

func FoundryTokenCredential() *azidentity.DefaultAzureCredential {
	if FoundryProjectEndpoint == "" {
		Panic("FOUNDRY_PROJECT_ENDPOINT environment variable is not set.")
	}
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		Panicf("failed to create Azure default credential: %v", err)
	}
	return token
}
