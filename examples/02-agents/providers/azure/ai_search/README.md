# Azure AI Search

Stores text embeddings in a temporary Azure AI Search index, then runs vector and
hybrid searches with a metadata filter. The index is deleted on exit; the Search
service and Foundry deployments are left untouched.

## Run

Sign in with `az login` and configure existing resources:

```powershell
$env:AZURE_AI_SEARCH_ENDPOINT = "https://<service>.search.windows.net"
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<resource>.services.ai.azure.com/api/projects/<project>"
$env:FOUNDRY_EMBEDDING_MODEL = "text-embedding-3-large"

go run .\examples\02-agents\providers\azure\ai_search
```

Authentication uses the shared `demo.FoundryTokenCredential` helper
(`DefaultAzureCredential`), like the neighboring Foundry examples.

Search access requires permission to manage indexes and read/write their
documents, such as **Search Service Contributor** and **Search Index Data
Contributor**, along with access to the Foundry embedding deployment.

The schema expects **3072-dimensional embeddings**. Runs incur embedding usage;
the Search service may also incur charges depending on its tier. A Free Search
service is sufficient for this sample.
