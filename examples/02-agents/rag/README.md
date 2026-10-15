# Retrieval-augmented generation (RAG)

These examples follow the five [.NET AgentWithRAG samples](https://github.com/microsoft/agent-framework/tree/0c9944cc9f577d51277ac7c55dbc388b60a577af/dotnet/samples/02-agents/AgentWithRAG)
at revision `0c9944c`, preserving their source documents, questions, retrieval
settings, and context/history behavior.

| Example | Retrieval | Prerequisites |
| --- | --- | --- |
| [Step 01: Basic text RAG](./step01_basic_text_rag/) | In-memory cosine search; one result per query | Foundry chat and embedding deployments |
| [Step 02: Custom vector store RAG](./step02_custom_vector_store_rag/) | Qdrant; five results; five recent messages in the query | Foundry deployments and Qdrant at `localhost:6334` |
| [Step 03: Custom RAG data source](./step03_custom_rag_data_source/) | Mock keyword search; six recent messages in the query | Foundry chat deployment |
| [Step 04: Foundry service RAG](./step04_foundry_service_rag/) | Server-side file search over an uploaded knowledge base | Foundry chat deployment and permission to create files, vector stores, and agents |
| [Step 05: Neo4j graph RAG](./step05_neo4j_graph_rag/) | Full-text search followed by graph enrichment; five results | Foundry chat deployment and a populated Neo4j graph/full-text index |

## Configuration

Set the Foundry project endpoint and sign in using a credential supported by
`DefaultAzureCredential`, such as `az login`:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<resource>.services.ai.azure.com/api/projects/<project>"
$env:FOUNDRY_MODEL = "gpt-5.4-mini"
$env:FOUNDRY_EMBEDDING_MODEL = "text-embedding-3-large"
```

The examples use the shared `demo.FoundryModel`, which defaults to `gpt-4o-mini`
when `FOUNDRY_MODEL` is unset or blank. Set it as above to match the upstream
sample's `gpt-5.4-mini` choice. Steps 01 and 02 also use
`FOUNDRY_EMBEDDING_MODEL`. Set it to `text-embedding-3-large` as above for their
3072-dimensional vector schemas. The shared demo default, `text-embedding-ada-002`,
produces 1536-dimensional vectors; using it requires changing the corresponding
store/schema dimensions to 1536. Deployment names must match those in your resource.

For production, choose a specific credential appropriate to the deployment,
rather than the development-oriented `DefaultAzureCredential` chain.

Run an example from the repository root:

```powershell
go run .\examples\02-agents\rag\step01_basic_text_rag
go run .\examples\02-agents\rag\step02_custom_vector_store_rag
go run .\examples\02-agents\rag\step03_custom_rag_data_source
go run .\examples\02-agents\rag\step04_foundry_service_rag
go run .\examples\02-agents\rag\step05_neo4j_graph_rag
```

These commands invoke live services and may incur charges.

### Qdrant

Start a disposable local Qdrant instance with its gRPC port exposed, for example:

```powershell
docker run --rm -p 6333:6333 -p 6334:6334 qdrant/qdrant
```

**Step 02 deletes and recreates the `documentation` collection on every run,**
matching the upstream sample. Do not point it at a collection containing data
you want to retain. It downloads the same two Agent Framework documentation
pages, splits them into 2000 UTF-16 code units with 200 units of overlap, and
stores the text, source metadata, and an unnamed cosine vector in Qdrant.

### Foundry service RAG

Step 04 embeds the upstream
[knowledge-base file](./step04_foundry_service_rag/contoso-outdoors-knowledge-base.md),
so it works independently of the current working directory.

It creates a file, a vector store, and a version of the agent named `AskContoso`.
It invokes that exact version through the project Responses API using an
`agent_reference`, without overriding server-owned instructions or tools.

**The example deletes `AskContoso` during cleanup, including its versions.**
Run it only in a project where this name is disposable. Cleanup also removes
the uploaded file and vector store. Go defers additionally attempt cleanup
after partial failures and cancellation; cleanup failures remain visible.

### Neo4j

```powershell
$env:NEO4J_URI = "neo4j://localhost:7687"
$env:NEO4J_USERNAME = "neo4j"
$env:NEO4J_PASSWORD = "<password>"
$env:NEO4J_FULLTEXT_INDEX_NAME = "search_chunks"
```

The username and index name default to `neo4j` and `search_chunks`. The graph
must already contain the same document/company/product/risk schema used by
the upstream sample, with a full-text index over the chunk text. The example
does not create the graph or seed data. See the
[upstream Neo4j integration](https://github.com/neo4j-labs/neo4j-maf-provider/tree/b339a2d75e45c358886fe07fd1dd2ccf6f832423/dotnet)
for graph setup guidance.

The provider uses the ten most recent nonblank user/assistant messages,
the upstream stop-word list, read-routed Cypher, limits before and after graph
enrichment, and the same score/metadata formatting. It does not generate
embeddings for this full-text sample.

## Go implementation notes

- Each example contains its own instructions, questions, and conversation loop;
  there is no shared RAG support package. Step 01 keeps its in-memory store
  alongside its entrypoint.
- Examples use the shared `demo.NewLogger` middleware and `demo.Response` output.
  `demo.FoundryEmbeddingModel` holds the embedding deployment resolved at startup.
- Model-backed agents use `foundryprovider.NewAgent`. Local conversation history
  is enabled explicitly with `DisableStoreOutput`; Steps 01 and 02 exclude
  retrieved context from retained history.
- Steps 01 and 02 use `foundryprovider.NewEmbeddingGenerator` for embeddings.
  It derives the resource OpenAI endpoint from the project URL, configures
  Foundry authentication, and delegates to the OpenAI embedding adapter.
- The existing project client does not expose file/vector-store operations or
  agent-version administration. Step 04 uses `foundryprovider.NewOpenAIClient`
  for those operations, retaining Azure credential refresh and origin protections.
- Step 04 uses the native SDK's generic HTTP methods for agent administration
  and an explicit version reference, rather than attaching to a potentially
  different version selected by an agent endpoint.
- The sample-only in-memory `TextSearchStore` implements the path exercised by
  Step 01. Its default cosine similarity and descending top-k ranking were
  verified against CommunityToolkit.VectorData.InMemory 1.0.1. It is not a port
  of the entire generic vector-store framework or the helper's unused
  hybrid-search, namespace-filter, or external-text-hydration options.
- Qdrant and Neo4j use native Go connectors, not mock substitutes.
- Neo4j retrieval helpers follow `Neo4j.AgentFramework.GraphRAG`
  `0.1.0-preview.2`; its MIT notice is included in
  [LICENSE.neo4j](./step05_neo4j_graph_rag/LICENSE.neo4j).

## Local validation

```powershell
go build .\examples\02-agents\rag\...
go vet .\examples\02-agents\rag\...
```

These checks do not require credentials or running Qdrant, Neo4j, or Foundry
services.
