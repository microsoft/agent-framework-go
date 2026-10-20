// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

type cosmosParameter struct {
	Name  string
	Value any
}

func (c *cosmosCollection) translateFilter(filter *vectordata.Filter) (string, []cosmosParameter, error) {
	if err := filter.Validate(); err != nil {
		return "", nil, err
	}
	if filter == nil {
		return "", nil, nil
	}
	var parameters []cosmosParameter
	next := 0
	var translate func(*vectordata.Filter) (string, error)
	translate = func(node *vectordata.Filter) (string, error) {
		switch node.Operator {
		case vectordata.And, vectordata.Or:
			parts := make([]string, len(node.Operands))
			for i := range node.Operands {
				part, err := translate(&node.Operands[i])
				if err != nil {
					return "", err
				}
				parts[i] = part
			}
			return "(" + strings.Join(parts, " "+strings.ToUpper(string(node.Operator))+" ") + ")", nil
		case vectordata.Not:
			part, err := translate(&node.Operands[0])
			if err != nil {
				return "", err
			}
			return "(NOT " + part + ")", nil
		}
		property, found := c.properties[node.Property]
		if !found {
			return "", fmt.Errorf("filter property %q was not found", node.Property)
		}
		if _, ok := property.(*providerservices.VectorPropertyModel); ok {
			return "", fmt.Errorf("filter property %q is a vector, not a data property", node.Property)
		}
		typ := cosmosFilterType(property)
		expression := "c[" + cosmosJSONString(propertyStorageName(property)) + "]"
		parameterValue := func(value any) any {
			if _, ok := property.(*providerservices.KeyPropertyModel); ok {
				switch key := value.(type) {
				case uuid.UUID:
					return key.String()
				case CosmosKey:
					return key.DocumentID
				}
			}
			return value
		}
		valueName := func(value any) string {
			name := "@p" + strconv.Itoa(next)
			next++
			parameters = append(parameters, cosmosParameter{Name: name, Value: parameterValue(value)})
			return name
		}
		switch node.Operator {
		case vectordata.Equal, vectordata.NotEqual:
			if node.Value == nil {
				if node.Operator == vectordata.Equal {
					return "IS_NULL(" + expression + ")", nil
				}
				return "NOT IS_NULL(" + expression + ")", nil
			}
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
				return "", fmt.Errorf("filter property %q is a collection; scalar comparison is unsupported", node.Property)
			}
			if !cosmosFilterValueCompatible(typ, node.Value) {
				return "", fmt.Errorf("filter value for property %q has incompatible type %T", node.Property, node.Value)
			}
			operator := "="
			if node.Operator == vectordata.NotEqual {
				operator = "<>"
			}
			return expression + " " + operator + " " + valueName(node.Value), nil
		case vectordata.GreaterThan:
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || !cosmosFilterValueCompatible(typ, node.Value) {
				return "", fmt.Errorf("filter value for property %q has incompatible type %T", node.Property, node.Value)
			}
			return expression + " > " + valueName(node.Value), nil
		case vectordata.GreaterThanOrEqual:
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || !cosmosFilterValueCompatible(typ, node.Value) {
				return "", fmt.Errorf("filter value for property %q has incompatible type %T", node.Property, node.Value)
			}
			return expression + " >= " + valueName(node.Value), nil
		case vectordata.LessThan:
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || !cosmosFilterValueCompatible(typ, node.Value) {
				return "", fmt.Errorf("filter value for property %q has incompatible type %T", node.Property, node.Value)
			}
			return expression + " < " + valueName(node.Value), nil
		case vectordata.LessThanOrEqual:
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || !cosmosFilterValueCompatible(typ, node.Value) {
				return "", fmt.Errorf("filter value for property %q has incompatible type %T", node.Property, node.Value)
			}
			return expression + " <= " + valueName(node.Value), nil
		case vectordata.Contains:
			if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
				return "", fmt.Errorf("filter property %q is not a collection", node.Property)
			}
			if !cosmosFilterValueCompatible(typ.Elem(), node.Value) {
				return "", fmt.Errorf("filter value for property %q has incompatible type %T", node.Property, node.Value)
			}
			return "ARRAY_CONTAINS(" + expression + ", " + valueName(node.Value) + ")", nil
		case vectordata.In:
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
				return "", fmt.Errorf("filter property %q is a collection; in filters require a scalar property", node.Property)
			}
			values := reflect.ValueOf(node.Value)
			normalized := make([]any, values.Len())
			for i := range values.Len() {
				value := values.Index(i).Interface()
				if !cosmosFilterValueCompatible(typ, value) {
					return "", fmt.Errorf("in filter value %d for property %q has incompatible type %T", i, node.Property, values.Index(i).Interface())
				}
				normalized[i] = parameterValue(value)
			}
			return "ARRAY_CONTAINS(" + valueName(normalized) + ", " + expression + ")", nil
		default:
			return "", fmt.Errorf("unsupported filter operator %q", node.Operator)
		}
	}
	sql, err := translate(filter)
	if err != nil {
		return "", nil, err
	}
	for i := range parameters {
		parameters[i].Value, err = cosmosDataJSON(parameters[i].Value)
		if err != nil {
			return "", nil, fmt.Errorf("filter parameter %q: %w", parameters[i].Name, err)
		}
	}
	return sql, parameters, nil
}

func cosmosFilterType(property providerservices.PropertyModel) reflect.Type {
	var typ reflect.Type
	switch p := property.(type) {
	case *providerservices.KeyPropertyModel:
		typ = p.Type
		if typ == reflect.TypeFor[uuid.UUID]() || typ == reflect.TypeFor[CosmosKey]() {
			typ = reflect.TypeFor[string]()
		}
	case *providerservices.DataPropertyModel:
		typ = p.Type
	}
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

func cosmosFilterValueCompatible(target reflect.Type, value any) bool {
	if target == nil || value == nil {
		return value == nil
	}
	if target.Kind() == reflect.String {
		switch value.(type) {
		case uuid.UUID, CosmosKey:
			return true
		}
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	valueType := reflect.TypeOf(value)
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if target == valueType || valueType.AssignableTo(target) {
		return true
	}
	numeric := func(kind reflect.Kind) bool {
		return kind >= reflect.Int && kind <= reflect.Float64
	}
	return numeric(target.Kind()) && numeric(valueType.Kind())
}

func cosmosJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (c *cosmosCollection) orderBy(orderBy []vectordata.Sort) (string, error) {
	parts := make([]string, len(orderBy))
	for i, order := range orderBy {
		property, found := c.properties[order.Property]
		if !found {
			return "", fmt.Errorf("sort property %q was not found", order.Property)
		}
		if _, ok := property.(*providerservices.VectorPropertyModel); ok {
			return "", fmt.Errorf("sort property %q is a vector", order.Property)
		}
		direction := "ASC"
		if order.Descending {
			direction = "DESC"
		}
		parts[i] = "c[" + cosmosJSONString(propertyStorageName(property)) + "] " + direction
	}
	return strings.Join(parts, ", "), nil
}

func (c *cosmosCollection) queryPartition(filter *vectordata.Filter, required bool) (azcosmos.PartitionKey, error) {
	values := make([]any, len(c.partitionProperties))
	for i, property := range c.partitionProperties {
		name := propertyName(property)
		value, found := cosmosPartitionFilterValue(filter, name)
		if !found {
			if required {
				return azcosmos.PartitionKey{}, fmt.Errorf("azcosmos requires equality filters for every partition key property on ranked or ordered queries; missing property %q", name)
			}
			return azcosmos.NewPartitionKey(), nil
		}
		if property == c.key && value != nil {
			id, err := cosmosID(value)
			if err != nil {
				return azcosmos.PartitionKey{}, err
			}
			value = id
		}
		normalized, err := normalizePartitionValue(value)
		if err != nil {
			return azcosmos.PartitionKey{}, fmt.Errorf("partition key filter %q: %w", name, err)
		}
		values[i] = normalized
	}
	return cosmosPartitionKey(values)
}

func cosmosPartitionFilterValue(filter *vectordata.Filter, property string) (any, bool) {
	if filter == nil {
		return nil, false
	}
	switch filter.Operator {
	case vectordata.Equal:
		if filter.Property == property {
			return filter.Value, true
		}
	case vectordata.And:
		for i := range filter.Operands {
			if value, found := cosmosPartitionFilterValue(&filter.Operands[i], property); found {
				return value, true
			}
		}
	}
	return nil, false
}

func (c *cosmosCollection) Search(ctx context.Context, value any, top int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return func(yield func(vectordata.SearchResult[any], error) bool) {
		if err := ctx.Err(); err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
			return
		}
		var opts vectordata.SearchOptions
		if options != nil {
			opts = *options
			if err := opts.Validate(); err != nil {
				yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
				return
			}
			if err := c.validateRetrieval(opts.IncludeVectors); err != nil {
				yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
				return
			}
		}
		if top <= 0 {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", errors.New("top must be positive")))
			return
		}
		property, err := c.model.VectorProperty(opts.VectorProperty)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
			return
		}
		filter, parameters, err := c.translateFilter(opts.Filter)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
			return
		}
		partition, err := c.queryPartition(opts.Filter, true)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
			return
		}
		vector, err := c.vectorForSearch(ctx, property, value)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
			return
		}
		query, parameters, err := c.buildSearchQuery(property, vector, nil, nil, filter, parameters, top, opts.Skip, opts.ScoreThreshold, opts.IncludeVectors)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
			return
		}
		err = c.eachQueryDocument(ctx, query, parameters, partition, func(raw json.RawMessage) bool {
			result, err := c.decodeSearchResult(raw, opts.IncludeVectors)
			if err != nil {
				yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
				return false
			}
			return yield(result, nil)
		})
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "Search", err))
		}
	}
}

func (c *cosmosCollection) HybridSearch(ctx context.Context, value any, keywords []string, top int, options *vectordata.HybridSearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return func(yield func(vectordata.SearchResult[any], error) bool) {
		if err := ctx.Err(); err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		var opts vectordata.HybridSearchOptions
		if options != nil {
			opts = *options
			if err := opts.Validate(); err != nil {
				yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
				return
			}
			if err := c.validateRetrieval(opts.IncludeVectors); err != nil {
				yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
				return
			}
		}
		if top <= 0 {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", errors.New("top must be positive")))
			return
		}
		vectorProperty, err := c.model.VectorProperty(opts.VectorProperty)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		textProperty, err := c.model.FullTextProperty(opts.AdditionalProperty)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		filter, parameters, err := c.translateFilter(opts.Filter)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		partition, err := c.queryPartition(opts.Filter, true)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		vector, err := c.vectorForSearch(ctx, vectorProperty, value)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		query, parameters, err := c.buildSearchQuery(vectorProperty, vector, textProperty, keywords, filter, parameters, top, opts.Skip, opts.ScoreThreshold, opts.IncludeVectors)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
			return
		}
		err = c.eachQueryDocument(ctx, query, parameters, partition, func(raw json.RawMessage) bool {
			result, err := c.decodeSearchResult(raw, opts.IncludeVectors)
			if err != nil {
				yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
				return false
			}
			return yield(result, nil)
		})
		if err != nil {
			yield(vectordata.SearchResult[any]{}, cosmosFailure(c.store.database, c.name, "HybridSearch", err))
		}
	}
}

func (c *cosmosCollection) selectFields(includeVectors bool) []string {
	fields := make([]string, 0, len(c.model.Properties)+1)
	for _, property := range c.model.Properties {
		if _, isVector := property.(*providerservices.VectorPropertyModel); isVector && !includeVectors {
			continue
		}
		fields = append(fields, "c["+cosmosJSONString(propertyStorageName(property))+"]")
	}
	return fields
}

func (c *cosmosCollection) buildSearchQuery(
	vectorProperty *providerservices.VectorPropertyModel,
	vector any,
	textProperty *providerservices.DataPropertyModel,
	keywords []string,
	filter string,
	parameters []cosmosParameter,
	top, skip int,
	scoreThreshold *float64,
	includeVectors bool,
) (string, []cosmosParameter, error) {
	vectorExpression := "VectorDistance(c[" + cosmosJSONString(vectorProperty.StorageName) + "], @vector)"
	parameters = append(parameters, cosmosParameter{Name: "@vector", Value: vector})
	selectFields := c.selectFields(includeVectors)
	selectFields = append(selectFields, vectorExpression+" AS "+cosmosScoreField)
	rankingExpression := vectorExpression
	if textProperty != nil && keywords != nil {
		fullTextArguments := []string{"c[" + cosmosJSONString(textProperty.StorageName) + "]"}
		for i, keyword := range keywords {
			name := "@keyword" + strconv.Itoa(i)
			fullTextArguments = append(fullTextArguments, name)
			parameters = append(parameters, cosmosParameter{Name: name, Value: keyword})
		}
		rankingExpression = "RANK RRF(" + vectorExpression + ", FullTextScore(" + strings.Join(fullTextArguments, ", ") + "))"
	}
	conditions := make([]string, 0, 2)
	if filter != "" {
		conditions = append(conditions, filter)
	}
	if scoreThreshold != nil {
		operator := ">="
		switch vectorProperty.DistanceFunction {
		case "", vectordata.CosineSimilarity, vectordata.DotProductSimilarity:
		case vectordata.EuclideanDistance:
			operator = "<="
		default:
			return "", nil, fmt.Errorf("score threshold is unsupported for distance function %q", vectorProperty.DistanceFunction)
		}
		conditions = append(conditions, vectorExpression+" "+operator+" @scoreThreshold")
		parameters = append(parameters, cosmosParameter{Name: "@scoreThreshold", Value: *scoreThreshold})
	}
	topClause := fmt.Sprintf("TOP %d ", top)
	var query strings.Builder
	query.WriteString("SELECT ")
	query.WriteString(topClause)
	query.WriteString(strings.Join(selectFields, ", "))
	query.WriteString(" FROM c")
	if len(conditions) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(conditions, " AND "))
	}
	query.WriteString(" ORDER BY ")
	query.WriteString(rankingExpression)
	if skip > 0 {
		query.Reset()
		query.WriteString("SELECT ")
		query.WriteString(strings.Join(selectFields, ", "))
		query.WriteString(" FROM c")
		if len(conditions) > 0 {
			query.WriteString(" WHERE ")
			query.WriteString(strings.Join(conditions, " AND "))
		}
		query.WriteString(" ORDER BY ")
		query.WriteString(rankingExpression)
		fmt.Fprintf(&query, " OFFSET %d LIMIT %d", skip, top)
	}
	return query.String(), parameters, nil
}

func (c *cosmosCollection) decodeSearchResult(raw json.RawMessage, includeVectors bool) (vectordata.SearchResult[any], error) {
	document, err := rawDocument(raw)
	if err != nil {
		return vectordata.SearchResult[any]{}, err
	}
	var score *float64
	if rawScore, found := document[cosmosScoreField]; found && !bytes.Equal(bytes.TrimSpace(rawScore), []byte("null")) {
		var value float64
		if err := json.Unmarshal(rawScore, &value); err != nil {
			return vectordata.SearchResult[any]{}, fmt.Errorf("invalid Cosmos DB score: %w", err)
		}
		score = &value
	}
	record, err := c.decodeRecord(document, includeVectors)
	if err != nil {
		return vectordata.SearchResult[any]{}, err
	}
	return vectordata.SearchResult[any]{Record: record, Score: score}, nil
}
