// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

type collection struct {
	store      *store
	name       string
	recordType reflect.Type
	model      *providerservices.CollectionModel
	key        *providerservices.KeyPropertyModel
	data       []*providerservices.DataPropertyModel
	vectors    []*providerservices.VectorPropertyModel
	properties map[string]providerservices.PropertyModel
}

func newCollection(s *store, name string, keyType, recordType reflect.Type, definition *vectordata.Definition) (*collection, error) {
	if strings.TrimSpace(name) == "" {
		return nil, failure(name, "Collection", fmt.Errorf("collection name is required"))
	}
	model, err := providerservices.Build(keyType, recordType, definition, providerservices.Config{
		SupportsMultipleVectors:   true,
		SupportsKeyType:           keyTypeSupported,
		SupportsDataType:          dataTypeSupported,
		SupportsVectorType:        providerservices.SupportsVector[float32],
		SupportsKeyAutoGeneration: func(typ reflect.Type) bool { return typ == reflect.TypeFor[uuid.UUID]() },
		DefaultEmbeddingGenerator: s.generator,
	})
	if err != nil {
		return nil, failure(name, "Collection", err)
	}
	c := &collection{
		store: s, name: name, recordType: recordType, model: model,
		properties: make(map[string]providerservices.PropertyModel, len(model.Properties)),
	}
	for _, property := range model.Properties {
		var propertyName, storageName string
		switch p := property.(type) {
		case *providerservices.KeyPropertyModel:
			c.key = p
			propertyName, storageName = p.Name, p.StorageName
		case *providerservices.DataPropertyModel:
			c.data = append(c.data, p)
			propertyName, storageName = p.Name, p.StorageName
		case *providerservices.VectorPropertyModel:
			c.vectors = append(c.vectors, p)
			propertyName, storageName = p.Name, p.StorageName
		}
		if !validIdentifier(storageName) {
			return nil, failure(name, "Collection", fmt.Errorf("property %q has invalid OData storage name %q", propertyName, storageName))
		}
		c.properties[propertyName] = property
	}
	return c, nil
}

func keyTypeSupported(typ reflect.Type) bool {
	return typ.Kind() == reflect.String || typ == reflect.TypeFor[uuid.UUID]()
}

func dataTypeSupported(typ reflect.Type) bool {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[time.Time]() {
		return true
	}
	switch typ.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func validIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

type indexDefinition struct {
	Name         string                `json:"name"`
	Fields       []indexField          `json:"fields"`
	VectorSearch vectorIndexDefinition `json:"vectorSearch"`
}

type indexField struct {
	Name                string `json:"name"`
	Type                string `json:"type"`
	Key                 bool   `json:"key,omitempty"`
	Searchable          bool   `json:"searchable"`
	Filterable          bool   `json:"filterable"`
	Sortable            bool   `json:"sortable"`
	Facetable           bool   `json:"facetable"`
	Retrievable         bool   `json:"retrievable"`
	Dimensions          int    `json:"dimensions,omitempty"`
	VectorSearchProfile string `json:"vectorSearchProfile,omitempty"`
}

type vectorIndexDefinition struct {
	Algorithms []vectorAlgorithm `json:"algorithms"`
	Profiles   []vectorProfile   `json:"profiles"`
}

type vectorAlgorithm struct {
	Name                    string            `json:"name"`
	Kind                    string            `json:"kind"`
	HNSWParameters          *vectorParameters `json:"hnswParameters,omitempty"`
	ExhaustiveKNNParameters *vectorParameters `json:"exhaustiveKnnParameters,omitempty"`
}

type vectorParameters struct {
	Metric string `json:"metric"`
}

type vectorProfile struct {
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"`
}

func (c *collection) indexDefinition() (*indexDefinition, error) {
	definition := &indexDefinition{
		Name: c.name, Fields: make([]indexField, 0, len(c.model.Properties)),
		VectorSearch: vectorIndexDefinition{Algorithms: []vectorAlgorithm{}, Profiles: []vectorProfile{}},
	}
	for _, property := range c.model.Properties {
		field := indexField{Retrievable: true}
		switch p := property.(type) {
		case *providerservices.KeyPropertyModel:
			field.Name, field.Type = p.StorageName, "Edm.String"
			field.Key, field.Searchable, field.Filterable, field.Sortable = true, true, true, true
		case *providerservices.DataPropertyModel:
			field.Name = p.StorageName
			var err error
			field.Type, err = dataFieldType(p.Type)
			if err != nil {
				return nil, err
			}
			field.Filterable = p.IsIndexed
			field.Sortable = p.IsIndexed && !strings.HasPrefix(field.Type, "Collection(")
			if p.IsFullTextIndexed {
				typ := p.Type
				if typ.Kind() == reflect.Pointer {
					typ = typ.Elem()
				}
				if typ.Kind() != reflect.String {
					return nil, fmt.Errorf("property %q: full-text indexing requires a string", p.Name)
				}
				field.Searchable = true
			}
		case *providerservices.VectorPropertyModel:
			field.Name, field.Type = p.StorageName, "Collection(Edm.Single)"
			field.Searchable, field.Dimensions = true, p.Dimensions
			field.VectorSearchProfile = p.StorageName + "Profile"
			metric := ""
			switch p.DistanceFunction {
			case "", vectordata.CosineSimilarity:
				metric = "cosine"
			case vectordata.DotProductSimilarity:
				metric = "dotProduct"
			case vectordata.EuclideanDistance:
				metric = "euclidean"
			default:
				return nil, fmt.Errorf("property %q: unsupported distance function %q", p.Name, p.DistanceFunction)
			}
			algorithm := vectorAlgorithm{Name: p.StorageName + "AlgoConfig"}
			switch p.IndexKind {
			case "", vectordata.HNSW:
				algorithm.Kind, algorithm.HNSWParameters = "hnsw", &vectorParameters{Metric: metric}
			case vectordata.Flat:
				algorithm.Kind, algorithm.ExhaustiveKNNParameters = "exhaustiveKnn", &vectorParameters{Metric: metric}
			default:
				return nil, fmt.Errorf("property %q: unsupported index kind %q", p.Name, p.IndexKind)
			}
			definition.VectorSearch.Algorithms = append(definition.VectorSearch.Algorithms, algorithm)
			definition.VectorSearch.Profiles = append(definition.VectorSearch.Profiles, vectorProfile{
				Name: field.VectorSearchProfile, Algorithm: algorithm.Name,
			})
		}
		definition.Fields = append(definition.Fields, field)
	}
	return definition, nil
}

func dataFieldType(typ reflect.Type) (string, error) {
	if typ.Kind() == reflect.Pointer {
		return dataFieldType(typ.Elem())
	}
	if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		element, err := dataFieldType(typ.Elem())
		if err != nil {
			return "", err
		}
		return "Collection(" + element + ")", nil
	}
	if typ == reflect.TypeFor[time.Time]() {
		return "Edm.DateTimeOffset", nil
	}
	switch typ.Kind() {
	case reflect.String:
		return "Edm.String", nil
	case reflect.Bool:
		return "Edm.Boolean", nil
	case reflect.Int32:
		return "Edm.Int32", nil
	case reflect.Int, reflect.Int64:
		return "Edm.Int64", nil
	case reflect.Float32, reflect.Float64:
		return "Edm.Double", nil
	default:
		return "", fmt.Errorf("unsupported data property type %v", typ)
	}
}
