// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"reflect"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

const cosmosScoreField = "__cosmos_score"

type cosmosCollection struct {
	store               *cosmosDBStore
	container           *azcosmos.ContainerClient
	name                string
	recordType          reflect.Type
	model               *providerservices.CollectionModel
	key                 *providerservices.KeyPropertyModel
	data                []*providerservices.DataPropertyModel
	vectors             []*providerservices.VectorPropertyModel
	properties          map[string]providerservices.PropertyModel
	partitionProperties []providerservices.PropertyModel
}

func newCosmosCollection(store *cosmosDBStore, name string, keyType, recordType reflect.Type, definition *vectordata.Definition) (*cosmosCollection, error) {
	if err := validateCosmosName(name); err != nil {
		return nil, cosmosFailure(store.databaseName, name, "Collection", err)
	}
	modelKeyType := keyType
	if keyType == reflect.TypeFor[CosmosKey]() {
		// The composite lookup key is not the record's document ID.
		modelKeyType = reflect.TypeFor[any]()
	}
	model, err := providerservices.Build(modelKeyType, recordType, definition, providerservices.Config{
		SupportsMultipleVectors:   true,
		SupportsKeyType:           keyTypeSupported,
		SupportsDataType:          dataTypeSupported,
		SupportsVectorType:        cosmosVectorTypeSupported,
		SupportsKeyAutoGeneration: func(typ reflect.Type) bool { return typ == reflect.TypeFor[uuid.UUID]() },
		ReservedKeyStorageName:    "id",
		DefaultEmbeddingGenerator: store.generator,
	})
	if err != nil {
		return nil, cosmosFailure(store.databaseName, name, "Collection", err)
	}
	c := &cosmosCollection{
		store: store, name: name, recordType: recordType, model: model,
		properties: make(map[string]providerservices.PropertyModel, len(model.Properties)),
	}
	c.container, err = store.database.NewContainer(name)
	if err != nil {
		return nil, cosmosFailure(store.databaseName, name, "Collection", err)
	}
	for _, property := range model.Properties {
		switch p := property.(type) {
		case *providerservices.KeyPropertyModel:
			c.key = p
		case *providerservices.DataPropertyModel:
			c.data = append(c.data, p)
		case *providerservices.VectorPropertyModel:
			c.vectors = append(c.vectors, p)
		}
		c.properties[propertyName(property)] = property
	}
	if len(store.partitionProperties) != 0 {
		seen := make(map[string]struct{}, len(store.partitionProperties))
		for _, propertyName := range store.partitionProperties {
			property, found := c.properties[propertyName]
			if !found {
				return nil, cosmosFailure(store.databaseName, name, "Collection", fmt.Errorf("partition key property %q is not part of the record definition", propertyName))
			}
			storageName := propertyStorageName(property)
			if _, found := seen[storageName]; found {
				return nil, cosmosFailure(store.databaseName, name, "Collection", fmt.Errorf("partition key property %q is duplicated", propertyName))
			}
			seen[storageName] = struct{}{}
			switch p := property.(type) {
			case *providerservices.KeyPropertyModel:
				c.partitionProperties = append(c.partitionProperties, p)
			case *providerservices.DataPropertyModel:
				if err := validatePartitionType(p.Type); err != nil {
					return nil, cosmosFailure(store.databaseName, name, "Collection", fmt.Errorf("partition key property %q: %w", p.Name, err))
				}
				c.partitionProperties = append(c.partitionProperties, p)
			default:
				return nil, cosmosFailure(store.databaseName, name, "Collection", fmt.Errorf("partition key property %q must be a key or scalar data property", propertyName))
			}
		}
	}
	if len(c.partitionProperties) == 0 {
		c.partitionProperties = []providerservices.PropertyModel{c.key}
	}
	if keyType != reflect.TypeFor[CosmosKey]() && keyType != reflect.TypeFor[any]() && !c.partitionedByID() {
		return nil, cosmosFailure(store.databaseName, name, "Collection", errors.New("custom or hierarchical partition keys require CosmosKey as the collection lookup key type"))
	}
	for _, vector := range c.vectors {
		if _, _, err := c.vectorPolicy(vector); err != nil {
			return nil, cosmosFailure(store.database, name, "Collection", err)
		}
	}
	for _, property := range c.data {
		if property.IsFullTextIndexed {
			typ := property.Type
			if typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			if typ.Kind() != reflect.String {
				return nil, cosmosFailure(store.databaseName, name, "Collection", fmt.Errorf("full-text indexing requires string property %q", property.Name))
			}
		}
	}
	return c, nil
}

func validatePartitionType(typ reflect.Type) error {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[uuid.UUID]() {
		return nil
	}
	switch typ.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil
	default:
		return fmt.Errorf("unsupported Cosmos DB partition key type %v", typ)
	}
}

func propertyName(property providerservices.PropertyModel) string {
	switch p := property.(type) {
	case *providerservices.KeyPropertyModel:
		return p.Name
	case *providerservices.DataPropertyModel:
		return p.Name
	case *providerservices.VectorPropertyModel:
		return p.Name
	default:
		return ""
	}
}

func (c *cosmosCollection) Name() string { return c.name }

func (c *cosmosCollection) Exists(ctx context.Context) (bool, error) {
	_, err := c.container.Read(ctx, nil)
	if cosmosStatus(err, http.StatusNotFound) {
		return false, nil
	}
	if err != nil {
		return false, cosmosFailure(c.store.databaseName, c.name, "CollectionExists", err)
	}
	return true, nil
}

func (c *cosmosCollection) EnsureExists(ctx context.Context) error {
	properties, err := c.containerDefinition()
	if err != nil {
		return cosmosFailure(c.store.databaseName, c.name, "CreateCollection", err)
	}
	_, err = c.store.database.CreateContainer(ctx, properties, nil)
	if err != nil && !cosmosStatus(err, http.StatusConflict) {
		return cosmosFailure(c.store.databaseName, c.name, "CreateCollection", err)
	}
	return nil
}

func (c *cosmosCollection) EnsureDeleted(ctx context.Context) error {
	_, err := c.container.Delete(ctx, nil)
	if err != nil && !cosmosStatus(err, http.StatusNotFound) {
		return cosmosFailure(c.store.databaseName, c.name, "DeleteCollection", err)
	}
	return nil
}

func (c *cosmosCollection) containerDefinition() (azcosmos.ContainerProperties, error) {
	paths := make([]string, len(c.partitionProperties))
	for i, property := range c.partitionProperties {
		paths[i] = "/" + escapeCosmosPath(propertyStorageName(property))
	}
	kind := azcosmos.PartitionKeyKindHash
	if len(paths) > 1 {
		kind = azcosmos.PartitionKeyKindMultiHash
	}
	indexingPolicy := &azcosmos.IndexingPolicy{
		Automatic: c.store.automatic, IndexingMode: c.store.indexingMode,
		IncludedPaths: []azcosmos.IncludedPath{}, ExcludedPaths: []azcosmos.ExcludedPath{},
		VectorIndexes: []azcosmos.VectorIndex{}, FullTextIndexes: []azcosmos.FullTextIndex{},
	}
	properties := azcosmos.ContainerProperties{
		ID: c.name,
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{
			Kind: kind, Paths: paths, Version: 2,
		},
		IndexingPolicy: indexingPolicy,
	}
	if c.store.indexingMode == azcosmos.IndexingModeNone {
		return properties, nil
	}
	fullTextPaths := []azcosmos.FullTextPath{}
	embeddings := make([]azcosmos.VectorEmbedding, 0, len(c.vectors))
	for _, property := range c.vectors {
		index, embedding, err := c.vectorPolicy(property)
		if err != nil {
			return azcosmos.ContainerProperties{}, err
		}
		indexingPolicy.VectorIndexes = append(indexingPolicy.VectorIndexes, index)
		embeddings = append(embeddings, embedding)
		indexingPolicy.ExcludedPaths = append(indexingPolicy.ExcludedPaths, azcosmos.ExcludedPath{Path: "/" + escapeCosmosPath(property.StorageName) + "/*"})
	}
	for _, property := range c.data {
		if property.IsIndexed || property.IsFullTextIndexed {
			indexingPolicy.IncludedPaths = append(indexingPolicy.IncludedPaths, azcosmos.IncludedPath{Path: "/" + escapeCosmosPath(property.StorageName) + "/?"})
		}
		if property.IsFullTextIndexed {
			path := "/" + escapeCosmosPath(property.StorageName)
			indexingPolicy.FullTextIndexes = append(indexingPolicy.FullTextIndexes, azcosmos.FullTextIndex{Path: path})
			fullTextPaths = append(fullTextPaths, azcosmos.FullTextPath{Path: path, Language: "en-US"})
		}
	}
	indexingPolicy.IncludedPaths = append(indexingPolicy.IncludedPaths, azcosmos.IncludedPath{Path: "/*"})
	properties.VectorEmbeddingPolicy = &azcosmos.VectorEmbeddingPolicy{VectorEmbeddings: embeddings}
	properties.FullTextPolicy = &azcosmos.FullTextPolicy{DefaultLanguage: "en-US", FullTextPaths: fullTextPaths}
	return properties, nil
}

func (c *cosmosCollection) vectorPolicy(property *providerservices.VectorPropertyModel) (azcosmos.VectorIndex, azcosmos.VectorEmbedding, error) {
	path := "/" + escapeCosmosPath(property.StorageName)
	dataType, ok := cosmosVectorDataType(property.EmbeddingType)
	if !ok {
		return azcosmos.VectorIndex{}, azcosmos.VectorEmbedding{}, fmt.Errorf("property %q: unsupported Cosmos DB vector data type %v", property.Name, property.EmbeddingType)
	}
	indexType := azcosmos.VectorIndexTypeDiskANN
	switch strings.ToLower(string(property.IndexKind)) {
	case "", "diskann", "disk_ann":
	case "flat":
		indexType = azcosmos.VectorIndexTypeFlat
	case "quantizedflat", "quantized_flat":
		indexType = azcosmos.VectorIndexTypeQuantizedFlat
	default:
		return azcosmos.VectorIndex{}, azcosmos.VectorEmbedding{}, fmt.Errorf("property %q: unsupported Cosmos DB vector index kind %q; supported kinds are disk_ann, flat, and quantized_flat", property.Name, property.IndexKind)
	}
	distance := azcosmos.VectorDistanceFunctionCosine
	switch property.DistanceFunction {
	case "", vectordata.CosineSimilarity:
	case vectordata.DotProductSimilarity:
		distance = azcosmos.VectorDistanceFunctionDotProduct
	case vectordata.EuclideanDistance:
		distance = azcosmos.VectorDistanceFunctionEuclidean
	default:
		return azcosmos.VectorIndex{}, azcosmos.VectorEmbedding{}, fmt.Errorf("property %q: unsupported Cosmos DB distance function %q", property.Name, property.DistanceFunction)
	}
	return azcosmos.VectorIndex{Path: path, Type: indexType},
		azcosmos.VectorEmbedding{Path: path, DataType: dataType, Dimensions: int32(property.Dimensions), DistanceFunction: distance}, nil
}

func cosmosVectorDataType(typ reflect.Type) (azcosmos.VectorDataType, bool) {
	element, ok := cosmosVectorElementType(typ)
	if !ok {
		return "", false
	}
	switch element {
	case reflect.TypeFor[float32]():
		return azcosmos.VectorDataTypeFloat32, true
	case reflect.TypeFor[uint8]():
		return azcosmos.VectorDataTypeUint8, true
	case reflect.TypeFor[int8]():
		return azcosmos.VectorDataTypeInt8, true
	default:
		return "", false
	}
}

func escapeCosmosPath(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func propertyStorageName(property providerservices.PropertyModel) string {
	switch p := property.(type) {
	case *providerservices.KeyPropertyModel:
		return p.StorageName
	case *providerservices.DataPropertyModel:
		return p.StorageName
	case *providerservices.VectorPropertyModel:
		return p.StorageName
	default:
		return ""
	}
}

func (c *cosmosCollection) Get(ctx context.Context, key any, options *vectordata.RecordRetrievalOptions) (any, bool, error) {
	includeVectors := options != nil && options.IncludeVectors
	if err := c.validateRetrieval(includeVectors); err != nil {
		return nil, false, cosmosFailure(c.store.databaseName, c.name, "Get", err)
	}
	id, partitionValues, err := c.partitionForKey(key)
	if err != nil {
		return nil, false, cosmosFailure(c.store.databaseName, c.name, "Get", err)
	}
	partition, err := cosmosPartitionKey(partitionValues)
	if err != nil {
		return nil, false, cosmosFailure(c.store.databaseName, c.name, "Get", err)
	}
	response, err := c.container.ReadItem(ctx, partition, id, nil)
	if cosmosStatus(err, http.StatusNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, cosmosFailure(c.store.databaseName, c.name, "Get", err)
	}
	document, err := rawDocument(response.Value)
	if err != nil {
		return nil, false, cosmosFailure(c.store.databaseName, c.name, "Get", err)
	}
	record, err := c.decodeRecord(document, includeVectors)
	if err != nil {
		return nil, false, cosmosFailure(c.store.databaseName, c.name, "Get", err)
	}
	return record, true, nil
}

func (c *cosmosCollection) GetFiltered(ctx context.Context, filter vectordata.Filter, top int, options *vectordata.FilteredRecordRetrievalOptions) iter.Seq2[any, error] {
	return func(yield func(any, error) bool) {
		var zero any
		if err := ctx.Err(); err != nil {
			yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
			return
		}
		var opts vectordata.FilteredRecordRetrievalOptions
		if options != nil {
			opts = *options
			if err := opts.Validate(); err != nil {
				yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
				return
			}
			if err := c.validateRetrieval(opts.IncludeVectors); err != nil {
				yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
				return
			}
		}
		if top < 0 {
			yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", errors.New("top must be nonnegative")))
			return
		}
		if top == 0 {
			return
		}
		filterSQL, parameters, err := c.translateFilter(&filter)
		if err != nil {
			yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
			return
		}
		query := "SELECT VALUE c FROM c"
		if filterSQL != "" {
			query += " WHERE " + filterSQL
		}
		orderBy, err := c.orderBy(opts.OrderBy)
		if err != nil {
			yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
			return
		}
		partition, err := c.queryPartition(&filter, orderBy != "")
		if err != nil {
			yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
			return
		}
		if orderBy != "" {
			query += " ORDER BY " + orderBy
		}
		skip, remaining := opts.Skip, top
		err = c.eachQueryDocument(ctx, query, parameters, partition, func(raw json.RawMessage) bool {
			if skip > 0 {
				skip--
				return true
			}
			document, err := rawDocument(raw)
			if err != nil {
				yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
				return false
			}
			record, err := c.decodeRecord(document, opts.IncludeVectors)
			if err != nil {
				yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
				return false
			}
			remaining--
			return yield(record, nil) && remaining > 0
		})
		if err != nil {
			yield(zero, cosmosFailure(c.store.databaseName, c.name, "GetFiltered", err))
		}
	}
}

func (c *cosmosCollection) Delete(ctx context.Context, key any) error {
	id, partitionValues, err := c.partitionForKey(key)
	if err != nil {
		return cosmosFailure(c.store.databaseName, c.name, "Delete", err)
	}
	partition, err := cosmosPartitionKey(partitionValues)
	if err != nil {
		return cosmosFailure(c.store.databaseName, c.name, "Delete", err)
	}
	_, err = c.container.DeleteItem(ctx, partition, id, nil)
	if err != nil && !cosmosStatus(err, http.StatusNotFound) {
		return cosmosFailure(c.store.databaseName, c.name, "Delete", err)
	}
	return nil
}

func (c *cosmosCollection) Upsert(ctx context.Context, records ...any) error {
	if len(records) == 0 {
		return nil
	}
	owned := make([]any, len(records))
	for i, record := range records {
		prepared, err := c.prepareRecord(record)
		if err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		owned[i] = prepared
	}
	generated := make(map[string][]any)
	for _, property := range c.vectors {
		if property.EmbeddingType == property.Type {
			continue
		}
		result, err := providerservices.GenerateEmbeddings(ctx, property, owned)
		if err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "Upsert", fmt.Errorf("property %q: %w", property.Name, err))
		}
		vectors := make([]any, len(result.Embeddings))
		for i, value := range result.Embeddings {
			vector, dimensions, ok := cosmosVectorSlice(value, property.EmbeddingType)
			if !ok || dimensions != property.Dimensions {
				return cosmosFailure(c.store.databaseName, c.name, "Upsert", fmt.Errorf("property %q: generated embedding %d must have dimension %d and a supported Cosmos DB vector type", property.Name, i, property.Dimensions))
			}
			vectors[i] = vector
		}
		generated[property.Name] = vectors
	}
	for i, record := range owned {
		if err := ctx.Err(); err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "Upsert", err)
		}
		document, err := c.encodeRecord(record, i, generated)
		if err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		id, _ := document["id"].(string)
		partition, err := c.partitionForRecord(record, id)
		if err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		partitionKey, err := cosmosPartitionKey(partition)
		if err == nil {
			var body []byte
			body, err = json.Marshal(document)
			if err == nil {
				_, err = c.container.UpsertItem(ctx, partitionKey, body, nil)
			}
		}
		if err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
	}
	return nil
}

func (c *cosmosCollection) GetBatch(ctx context.Context, keys []any, options *vectordata.RecordRetrievalOptions) iter.Seq2[any, error] {
	return func(yield func(any, error) bool) {
		if err := c.validateRetrieval(options != nil && options.IncludeVectors); err != nil {
			yield(nil, cosmosFailure(c.store.databaseName, c.name, "GetBatch", err))
			return
		}
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				yield(nil, cosmosFailure(c.store.databaseName, c.name, "GetBatch", err))
				return
			}
			record, found, err := c.Get(ctx, key, options)
			if err != nil {
				yield(nil, cosmosFailure(c.store.databaseName, c.name, "GetBatch", err))
				return
			}
			if found && !yield(record, nil) {
				return
			}
		}
	}
}

func (c *cosmosCollection) DeleteBatch(ctx context.Context, keys []any) error {
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "DeleteBatch", err)
		}
		if err := c.Delete(ctx, key); err != nil {
			return cosmosFailure(c.store.databaseName, c.name, "DeleteBatch", fmt.Errorf("key %d: %w", i, err))
		}
	}
	return nil
}

func (c *cosmosCollection) prepareRecord(record any) (any, error) {
	value := reflect.ValueOf(record)
	if !value.IsValid() || !value.Type().AssignableTo(c.recordType) {
		return nil, fmt.Errorf("record must have type %v, got %T", c.recordType, record)
	}
	if value.Kind() == reflect.Struct {
		owned := reflect.New(c.recordType)
		owned.Elem().Set(value)
		return owned.Interface(), nil
	}
	if value.IsNil() {
		return nil, errors.New("record must not be nil")
	}
	if value.Kind() == reflect.Pointer && value.Elem().Kind() == reflect.Map && value.Elem().IsNil() {
		return nil, errors.New("record map must not be nil")
	}
	return value.Interface(), nil
}

func (c *cosmosCollection) encodeRecord(record any, index int, generated map[string][]any) (map[string]any, error) {
	keyValue, found, err := c.key.Value(record)
	if err != nil {
		return nil, err
	}
	if c.key.IsAutoGenerated && (!found || keyValue == nil || keyValue == uuid.Nil) {
		keyValue, err = uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		if err := c.key.SetValue(record, keyValue); err != nil {
			return nil, err
		}
	}
	id, err := cosmosID(keyValue)
	if err != nil {
		return nil, fmt.Errorf("property %q: %w", c.key.Name, err)
	}
	document := make(map[string]any, len(c.model.Properties)+1)
	document["id"] = id
	for _, property := range c.data {
		value, found, err := property.Value(record)
		if err != nil {
			return nil, err
		}
		if found {
			document[property.StorageName] = value
		}
	}
	for _, property := range c.vectors {
		value, found, err := property.Value(record)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if outputs, ok := generated[property.Name]; ok {
			value = outputs[index]
		}
		vector, dimensions, ok := cosmosVectorSlice(value, property.EmbeddingType)
		if !ok {
			return nil, fmt.Errorf("property %q requires a supported Cosmos DB vector type", property.Name)
		}
		if vector != nil && dimensions != property.Dimensions {
			return nil, fmt.Errorf("property %q requires %d dimensions, got %d", property.Name, property.Dimensions, dimensions)
		}
		document[property.StorageName] = cosmosVectorJSON(vector)
	}
	return document, nil
}

func (c *cosmosCollection) decodeRecord(document map[string]json.RawMessage, includeVectors bool) (any, error) {
	if document == nil {
		return nil, errors.New("response document is missing")
	}
	root := reflect.New(c.recordType)
	target := root.Interface()
	recordType := c.recordType
	if recordType.Kind() == reflect.Pointer {
		recordType = recordType.Elem()
		root.Elem().Set(reflect.New(recordType))
		target = root.Elem().Interface()
	}
	if recordType.Kind() == reflect.Map {
		value := reflect.ValueOf(target).Elem()
		value.Set(reflect.MakeMap(recordType))
	}
	rawID, found := document["id"]
	if !found || bytes.Equal(bytes.TrimSpace(rawID), []byte("null")) {
		return nil, errors.New("response is missing document id")
	}
	var id string
	if err := json.Unmarshal(rawID, &id); err != nil || strings.TrimSpace(id) == "" {
		if err == nil {
			err = errors.New("document id must not be blank")
		}
		return nil, fmt.Errorf("property %q: %w", c.key.Name, err)
	}
	keyValue, err := c.decodedKey(id)
	if err != nil {
		return nil, err
	}
	if err := c.key.SetValue(target, keyValue); err != nil {
		return nil, err
	}
	for _, property := range c.data {
		if raw, found := document[property.StorageName]; found {
			value, err := decodeCosmosValue(raw, property.Type)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", property.Name, err)
			}
			if err := property.SetValue(target, value); err != nil {
				return nil, err
			}
		}
	}
	if includeVectors {
		for _, property := range c.vectors {
			if raw, found := document[property.StorageName]; found {
				value, err := decodeCosmosVector(raw, property)
				if err != nil {
					return nil, fmt.Errorf("property %q: %w", property.Name, err)
				}
				if err := property.SetValue(target, value); err != nil {
					return nil, err
				}
			}
		}
	}
	return root.Elem().Interface(), nil
}

func (c *cosmosCollection) decodedKey(id string) (any, error) {
	switch c.key.Type {
	case reflect.TypeFor[uuid.UUID]():
		value, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("property %q: invalid UUID document ID: %w", c.key.Name, err)
		}
		return value, nil
	default:
		return reflect.ValueOf(id).Convert(c.key.Type).Interface(), nil
	}
}

func decodeCosmosValue(raw json.RawMessage, typ reflect.Type) (any, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value := reflect.New(typ)
	if err := json.Unmarshal(raw, value.Interface()); err != nil {
		return nil, err
	}
	return value.Elem().Interface(), nil
}

func decodeCosmosVector(raw json.RawMessage, property *providerservices.VectorPropertyModel) (any, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if property.Type.Kind() == reflect.Pointer {
			return nil, nil
		}
		if isCosmosEmbeddingType(property.Type) {
			return reflect.Zero(property.Type).Interface(), nil
		}
		return nil, nil
	}
	if property.EmbeddingType != property.Type {
		return nil, errors.New("generated vector cannot be assigned to its non-vector input property")
	}
	if _, ok := cosmosVectorElementType(property.Type); !ok {
		return nil, fmt.Errorf("unsupported vector type %v", property.Type)
	}
	var decoded any
	typ := property.Type
	embeddingType := isCosmosEmbeddingType(typ)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if embeddingType {
		value := reflect.New(typ)
		vector := value.Elem().FieldByName("Vector")
		if !vector.IsValid() || !vector.CanAddr() {
			return nil, fmt.Errorf("unsupported embedding type %v", property.Type)
		}
		if err := json.Unmarshal(raw, vector.Addr().Interface()); err != nil {
			return nil, err
		}
		if property.Type.Kind() == reflect.Pointer {
			decoded = value.Interface()
		} else {
			decoded = value.Elem().Interface()
		}
	} else {
		value := reflect.New(typ)
		if err := json.Unmarshal(raw, value.Interface()); err != nil {
			return nil, err
		}
		decoded = value.Elem().Interface()
	}
	_, dimensions, ok := cosmosVectorSlice(decoded, property.Type)
	if !ok || dimensions != property.Dimensions {
		return nil, fmt.Errorf("expected a supported vector of dimension %d", property.Dimensions)
	}
	return decoded, nil
}

func cosmosVectorTypeSupported(typ reflect.Type) bool {
	return providerservices.SupportsVector[float32](typ) ||
		providerservices.SupportsVector[uint8](typ) ||
		providerservices.SupportsVector[int8](typ)
}

func cosmosVectorElementType(typ reflect.Type) (reflect.Type, bool) {
	switch typ {
	case reflect.TypeFor[[]float32](), reflect.TypeFor[embedding.VectorOf[float32]](),
		reflect.TypeFor[embedding.Of[float32]](), reflect.TypeFor[*embedding.Of[float32]]():
		return reflect.TypeFor[float32](), true
	case reflect.TypeFor[[]uint8](), reflect.TypeFor[embedding.VectorOf[uint8]](),
		reflect.TypeFor[embedding.Of[uint8]](), reflect.TypeFor[*embedding.Of[uint8]]():
		return reflect.TypeFor[uint8](), true
	case reflect.TypeFor[[]int8](), reflect.TypeFor[embedding.VectorOf[int8]](),
		reflect.TypeFor[embedding.Of[int8]](), reflect.TypeFor[*embedding.Of[int8]]():
		return reflect.TypeFor[int8](), true
	default:
		return nil, false
	}
}

func isCosmosEmbeddingType(typ reflect.Type) bool {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ == reflect.TypeFor[embedding.Of[float32]]() ||
		typ == reflect.TypeFor[embedding.Of[uint8]]() ||
		typ == reflect.TypeFor[embedding.Of[int8]]()
}

func cosmosVectorSlice(value any, typ reflect.Type) (any, int, bool) {
	if value == nil {
		return nil, 0, true
	}
	switch elementType, ok := cosmosVectorElementType(typ); {
	case ok && elementType == reflect.TypeFor[float32]():
		vector, supported := providerservices.AsVector[float32](value)
		if vector == nil {
			return nil, 0, supported
		}
		return vector, len(vector), supported
	case ok && elementType == reflect.TypeFor[uint8]():
		vector, supported := providerservices.AsVector[uint8](value)
		if vector == nil {
			return nil, 0, supported
		}
		return vector, len(vector), supported
	case ok && elementType == reflect.TypeFor[int8]():
		vector, supported := providerservices.AsVector[int8](value)
		if vector == nil {
			return nil, 0, supported
		}
		return vector, len(vector), supported
	default:
		return nil, 0, false
	}
}

func cosmosVectorJSON(vector any) any {
	if bytes, ok := vector.([]uint8); ok {
		values := make([]int, len(bytes))
		for i, value := range bytes {
			values[i] = int(value)
		}
		return values
	}
	return vector
}

func (c *cosmosCollection) partitionedByID() bool {
	return len(c.partitionProperties) == 1 && c.partitionProperties[0] == c.key
}

func (c *cosmosCollection) partitionForRecord(record any, id string) ([]any, error) {
	if c.partitionedByID() {
		return []any{id}, nil
	}
	values := make([]any, 0, len(c.partitionProperties))
	for _, property := range c.partitionProperties {
		switch property.(type) {
		case *providerservices.KeyPropertyModel:
			values = append(values, id)
		default:
			value, found, err := property.Value(record)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("partition key property %q is missing", propertyName(property))
			}
			normalized, err := normalizePartitionValue(value)
			if err != nil {
				return nil, fmt.Errorf("partition key property %q: %w", propertyName(property), err)
			}
			values = append(values, normalized)
		}
	}
	return values, nil
}

func normalizePartitionValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	valueOf := reflect.ValueOf(value)
	for valueOf.Kind() == reflect.Pointer {
		if valueOf.IsNil() {
			return nil, nil
		}
		valueOf = valueOf.Elem()
	}
	value = valueOf.Interface()
	if id, ok := value.(uuid.UUID); ok {
		return id.String(), nil
	}
	switch valueOf.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
	default:
		return nil, fmt.Errorf("unsupported partition key value type %T", value)
	}
	// Preserve the document's JSON number instead of widening float32 first.
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	switch normalized.(type) {
	case nil, string, bool, float64:
		return normalized, nil
	default:
		return nil, fmt.Errorf("partition key value of type %T must serialize to a JSON scalar", value)
	}
}

func (c *cosmosCollection) partitionForKey(key any) (string, []any, error) {
	id, err := cosmosID(key)
	if err != nil {
		return "", nil, err
	}
	cosmosKey, composite := key.(CosmosKey)
	if !composite {
		if c.partitionedByID() {
			return id, []any{id}, nil
		}
		return "", nil, errors.New("custom or hierarchical partition keys require a CosmosKey lookup key")
	}
	parts := cosmosKey.PartitionKey
	if len(parts) != len(c.partitionProperties) {
		return "", nil, fmt.Errorf("CosmosKey requires %d partition key values, got %d", len(c.partitionProperties), len(parts))
	}
	values := make([]any, len(parts))
	for i, part := range parts {
		normalized, err := normalizePartitionValue(part)
		if err != nil {
			return "", nil, fmt.Errorf("CosmosKey partition key value %d: %w", i, err)
		}
		values[i] = normalized
	}
	return id, values, nil
}

func cosmosPartitionKey(values []any) (azcosmos.PartitionKey, error) {
	key := azcosmos.NewPartitionKey()
	for _, value := range values {
		switch v := value.(type) {
		case nil:
			key = key.AppendNull()
		case string:
			key = key.AppendString(v)
		case bool:
			key = key.AppendBool(v)
		case float64:
			key = key.AppendNumber(v)
		default:
			return azcosmos.PartitionKey{}, fmt.Errorf("unsupported normalized partition key value %T", value)
		}
	}
	return key, nil
}

func (c *cosmosCollection) vectorForSearch(ctx context.Context, property *providerservices.VectorPropertyModel, value any) (any, error) {
	vector, dimensions, ok := cosmosVectorSlice(value, property.EmbeddingType)
	if !ok {
		generator := property.SearchEmbeddingGenerator
		if generator == nil {
			return nil, fmt.Errorf("property %q does not have a generator for search value type %T", property.Name, value)
		}
		result, err := generator.Generate(ctx, []any{value}, nil)
		if err != nil {
			return nil, err
		}
		if result == nil || len(result.Embeddings) != 1 {
			return nil, fmt.Errorf("property %q: search generator must return exactly one embedding", property.Name)
		}
		vector, dimensions, ok = cosmosVectorSlice(result.Embeddings[0], property.EmbeddingType)
		if !ok {
			return nil, fmt.Errorf("property %q: search generator must return a supported Cosmos DB vector type", property.Name)
		}
	}
	if dimensions != property.Dimensions {
		return nil, fmt.Errorf("property %q: search vector must have dimension %d, got %d", property.Name, property.Dimensions, dimensions)
	}
	return cosmosVectorJSON(vector), nil
}

func (c *cosmosCollection) validateRetrieval(includeVectors bool) error {
	if includeVectors && c.model.EmbeddingGenerationRequired {
		return errors.New("IncludeVectors is unsupported for collections with generated vector fields")
	}
	return nil
}

func (c *cosmosCollection) eachQueryDocument(ctx context.Context, query string, parameters []cosmosParameter, partition azcosmos.PartitionKey, yield func(json.RawMessage) bool) error {
	queryParameters := make([]azcosmos.QueryParameter, len(parameters))
	for i, parameter := range parameters {
		queryParameters[i] = azcosmos.QueryParameter{Name: parameter.Name, Value: parameter.Value}
	}
	pager := c.container.NewQueryItemsPager(query, partition, &azcosmos.QueryOptions{
		QueryParameters: queryParameters,
	})
	for pager.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, raw := range page.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !yield(raw) {
				return nil
			}
		}
	}
	return nil
}

func rawDocument(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return document, nil
}

var (
	_ vectordata.Collection[any, any]       = (*cosmosCollection)(nil)
	_ vectordata.KeywordHybridSearcher[any] = (*cosmosCollection)(nil)
)
