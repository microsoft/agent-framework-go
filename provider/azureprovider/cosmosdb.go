// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"reflect"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
)

// CosmosDBConfig configures container defaults shared by collections created
// from a Cosmos DB store. Create separate stores over the same client and
// database when different collections need different partition-key paths or
// indexing settings.
type CosmosDBConfig struct {
	// PartitionKeyProperties lists record property names to use as the
	// collection's partition-key paths, in hierarchical order. An empty list
	// uses the document ID (/id), so collection keys must be string or
	// uuid.UUID. A non-empty list uses up to three key or scalar data
	// properties and requires [CosmosKey] for point operations unless the
	// only partition property is the document ID. Record ID properties remain
	// strings or uuid.UUID values, independently of the collection's lookup
	// key type. Supported partition values are strings, booleans, numbers,
	// uuid.UUID values, and null, including nullable pointers.
	// Property names refer to the VectorData schema, not serialized field
	// names; their storage names become Cosmos DB partition paths. The list
	// is copied when the store is created.
	PartitionKeyProperties []string

	// IndexingMode selects the indexing mode for newly created containers. It
	// accepts "consistent", "lazy", or "none", case-insensitively; the default
	// is "consistent". "none" omits vector and full-text index policies.
	IndexingMode string

	// Automatic controls automatic indexing for newly created containers. Nil
	// defaults to true; a pointer preserves an explicit false.
	Automatic *bool

	// EmbeddingGenerator supplies the default generator for vector properties
	// in this store. A collection definition or property-specific generator
	// takes precedence.
	EmbeddingGenerator *embedding.BoundGenerator
}

// CosmosKey identifies a Cosmos DB document and its scalar or hierarchical
// partition key components when a collection uses custom partition paths.
// DocumentID must be nonblank. PartitionKey must contain one to three values
// matching [CosmosDBConfig.PartitionKeyProperties] in order. Components may
// be strings, booleans, numbers, [uuid.UUID] values, or nil; UUIDs are sent as
// strings. The record's key property stores only the document ID as a string
// or UUID, not a CosmosKey. Use a plain string or [uuid.UUID] lookup key when
// the collection uses the default /id partition path.
type CosmosKey struct {
	DocumentID   string
	PartitionKey []any
}

// CosmosDB manages a Cosmos DB NoSQL database through its embedded
// [vectordata.Store]. Create it with [NewCosmosDB].
//
// A Cosmos DB client and database must already exist. The store borrows the
// client and embedding generator and does not close either. The caller remains
// responsible for closing the client.
//
// Collections support string, [uuid.UUID], and [CosmosKey] lookup keys. Record
// ID properties are strings or UUIDs. With the default partition path, string
// and UUID lookup keys identify both the document and its partition; custom
// and hierarchical paths require CosmosKey unless the only partition property
// is the document ID. UUID IDs are generated when the key property enables
// auto-generation.
//
// Data properties support strings, booleans, signed integers, float32,
// float64, time.Time, nullable pointers to supported values, and slices or
// arrays of supported scalar values. Vector properties support float32, uint8,
// and int8 coordinates, including the corresponding [embedding.VectorOf] and
// [embedding.Of] types. Native vectors can be upserted directly; other vector
// properties require an embedding generator. Property and definition
// generators override the store default.
//
// Container policies include DiskANN, Flat, and QuantizedFlat vector indexes;
// DiskANN and cosine distance are defaults. Cosine, dot-product, and Euclidean
// distances are supported. Full-text indexing is available for string data
// properties with the en-US language policy. The account must have vector
// search enabled, and full-text queries require the corresponding account
// capability. Service constraints still apply; for example, small vector
// datasets may be scanned until an index can be built.
//
// Search supports vector and full-text/vector hybrid queries with portable
// filters. The native Go client does not support cross-partition ranking or
// ordering: search and ordered retrieval require equality filters for every
// partition-key property, optionally nested in conjunctions. Unsupported
// queries return an error before calling the service or embedding generator.
// Choose partition paths that group records searched together; with the
// default /id path, ranked queries must filter to a single document ID.
// Unordered filtered retrieval supports cross-partition queries and applies
// Skip and top while paging. Hybrid results are ranked with reciprocal-rank
// fusion and expose the raw vector distance as Score. Score thresholds compare
// against that distance (similarities use >=; Euclidean distance uses <=).
//
// IncludeVectors is unsupported on collections with generated vector fields,
// including missing records and empty query results. Nil native vectors are
// stored as null; non-nil vectors must have the configured dimensions.
//
// Item batches execute sequentially and may partially succeed. Generated
// input keys are not rolled back on failure. Per-collection partition and
// indexing settings are not available on the shared VectorData collection
// options; this provider configures those settings at store creation.
type CosmosDB struct {
	*vectordata.Store
}

type cosmosDBStore struct {
	database            *azcosmos.DatabaseClient
	databaseName        string
	generator           *embedding.BoundGenerator
	partitionProperties []string
	indexingMode        azcosmos.IndexingMode
	automatic           bool
}

// NewCosmosDB creates a store for databaseName using a caller-owned Cosmos DB
// client. The database must already exist; construction does not contact the
// service. The returned store borrows client and does not close it.
//
// Create and configure the client separately, so applications control its
// authentication and transport:
//
//	client, err := azcosmos.NewClient(endpoint, credential, nil)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	store, err := azureprovider.NewCosmosDB(client, "vector-database", azureprovider.CosmosDBConfig{})
//	if err != nil {
//	    return err
//	}
//	collection, err := vectordata.GetCollection[string, Record](store.Store, "records", nil)
//
// Use [vectordata.Collection.EnsureExists] to create the container and its
// indexing policies. It does not enable account-level vector or full-text
// search capabilities. The config's partition and indexing settings apply to
// all collections created from this store.
func NewCosmosDB(client *azcosmos.Client, databaseName string, config CosmosDBConfig) (*CosmosDB, error) {
	if client == nil {
		return nil, errors.New("azurecosmosdb: client is required")
	}
	if strings.TrimSpace(databaseName) == "" {
		return nil, errors.New("azurecosmosdb: database name is required")
	}
	if len(config.PartitionKeyProperties) > 3 {
		return nil, errors.New("azurecosmosdb: Cosmos DB supports at most 3 hierarchical partition key properties")
	}
	mode := strings.ToLower(strings.TrimSpace(config.IndexingMode))
	if mode == "" {
		mode = "consistent"
	}
	switch mode {
	case "consistent", "lazy", "none":
	default:
		return nil, fmt.Errorf("azurecosmosdb: unsupported indexing mode %q", config.IndexingMode)
	}
	database, err := client.NewDatabase(databaseName)
	if err != nil {
		return nil, err
	}
	automatic := true
	if config.Automatic != nil {
		automatic = *config.Automatic
	}
	indexingMode := azcosmos.IndexingModeConsistent
	switch mode {
	case "lazy":
		indexingMode = azcosmos.IndexingMode("Lazy")
	case "none":
		indexingMode = azcosmos.IndexingModeNone
	}
	provider := &cosmosDBStore{
		database: database, databaseName: databaseName,
		generator:           config.EmbeddingGenerator,
		partitionProperties: append([]string(nil), config.PartitionKeyProperties...),
		indexingMode:        indexingMode, automatic: automatic,
	}
	return &CosmosDB{Store: vectordata.NewStore(provider)}, nil
}

func (s *cosmosDBStore) OpenCollection(name string, keyType, recordType reflect.Type, definition *vectordata.Definition) (vectordata.Collection[any, any], error) {
	return newCosmosCollection(s, name, keyType, recordType, definition)
}

func (s *cosmosDBStore) ListCollectionNames(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		pager := s.database.NewQueryContainersPager("SELECT c.id FROM c", nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				yield("", cosmosFailure(s.databaseName, "", "ListCollectionNames", err))
				return
			}
			for _, container := range page.Containers {
				if err := ctx.Err(); err != nil {
					yield("", cosmosFailure(s.databaseName, "", "ListCollectionNames", err))
					return
				}
				if container.ID == "" {
					yield("", cosmosFailure(s.databaseName, "", "ListCollectionNames", errors.New("response contains a collection without an ID")))
					return
				}
				if !yield(container.ID, nil) {
					return
				}
			}
		}
	}
}

func (s *cosmosDBStore) CollectionExists(ctx context.Context, name string) (bool, error) {
	if err := validateCosmosName(name); err != nil {
		return false, cosmosFailure(s.databaseName, name, "CollectionExists", err)
	}
	container, err := s.database.NewContainer(name)
	if err != nil {
		return false, cosmosFailure(s.databaseName, name, "CollectionExists", err)
	}
	_, err = container.Read(ctx, nil)
	if cosmosStatus(err, http.StatusNotFound) {
		return false, nil
	}
	if err != nil {
		return false, cosmosFailure(s.databaseName, name, "CollectionExists", err)
	}
	return true, nil
}

func (s *cosmosDBStore) EnsureCollectionDeleted(ctx context.Context, name string) error {
	if err := validateCosmosName(name); err != nil {
		return cosmosFailure(s.databaseName, name, "DeleteCollection", err)
	}
	container, err := s.database.NewContainer(name)
	if err != nil {
		return cosmosFailure(s.databaseName, name, "DeleteCollection", err)
	}
	_, err = container.Delete(ctx, nil)
	if err != nil && !cosmosStatus(err, http.StatusNotFound) {
		return cosmosFailure(s.databaseName, name, "DeleteCollection", err)
	}
	return nil
}

func validateCosmosName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("collection name is required")
	}
	if strings.ContainsAny(name, "/\\?#") {
		return fmt.Errorf("invalid Cosmos DB collection name %q", name)
	}
	return nil
}

func cosmosFailure(database any, collection, operation string, err error) error {
	databaseName, _ := database.(string)
	if value, ok := database.(*azcosmos.DatabaseClient); ok && value != nil {
		databaseName = value.ID()
	}
	return &vectordata.Error{
		SystemName: "azure.cosmosdb", StoreName: databaseName, CollectionName: collection,
		OperationName: operation, Err: err,
	}
}

func cosmosStatus(err error, status int) bool {
	responseErr, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && responseErr.StatusCode == status
}

func cosmosID(key any) (string, error) {
	var id string
	switch value := key.(type) {
	case uuid.UUID:
		if value == uuid.Nil {
			return "", errors.New("key must not be nil")
		}
		id = value.String()
	case CosmosKey:
		if strings.TrimSpace(value.DocumentID) == "" {
			return "", errors.New("CosmosKey.DocumentID must not be blank")
		}
		id = value.DocumentID
	default:
		keyValue := reflect.ValueOf(key)
		if !keyValue.IsValid() || keyValue.Kind() != reflect.String {
			return "", fmt.Errorf("key must be a string, uuid.UUID, or CosmosKey, got %T", key)
		}
		id = keyValue.String()
	}
	if strings.TrimSpace(id) == "" {
		return "", errors.New("key must not be blank")
	}
	return id, nil
}

var _ vectordata.StoreProvider = (*cosmosDBStore)(nil)
