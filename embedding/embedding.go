// Copyright (c) Microsoft. All rights reserved.

// Package embedding defines provider-independent embedding generation contracts,
// vectors, request options, and results.
//
// Implement [Generator] to integrate an embedding service. Use [Generate] for
// a single input, [GenerateVector] for its vector, or [GenerateAndZip] to pair
// a batch of inputs with their embeddings. Generators and their wrappers must
// support concurrent calls; inputs and options must be treated as read-only.
//
// An [Embedding] contains metadata and a statically typed [Vector]. Numeric
// vectors use [VectorOf], and bit vectors use [Binary]. Their slices are borrowed,
// not copied. [Of] and [GeneratedOf] are shorthands for numeric embeddings and
// their batches. Embedding[Vector] holds vectors polymorphically. Embeddings
// support encoding/json directly in fields, slices, and [Generated] results.
package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/microsoft/agent-framework-go/message"
)

// Vector represents the data in an embedding.
type Vector interface {
	Dimensions() int
}

// Embedding contains a vector and metadata about its generation.
// TVector specifies the vector representation, such as VectorOf[float32] or
// Binary. Use Vector for a polymorphic vector. Its zero value uses TVector's
// zero value.
type Embedding[TVector Vector] struct {
	CreatedAt            time.Time      `json:",omitzero"`
	ModelID              string         `json:",omitzero"`
	AdditionalProperties map[string]any `json:",omitzero"`

	Vector TVector
}

// Of is an alias for Embedding[VectorOf[T]], an embedding with a numeric vector
// of T elements.
type Of[T any] = Embedding[VectorOf[T]]

// Dimensions returns the vector's dimensions, or zero if Vector is nil.
func (e *Embedding[TVector]) Dimensions() int {
	if any(e.Vector) == nil {
		return 0
	}
	return e.Vector.Dimensions()
}

// MarshalJSON encodes the embedding with a Type discriminator for VectorOf[uint8],
// VectorOf[int8], VectorOf[float32], VectorOf[float64], or Binary. Other vector
// types return an error. A nil Vector is encoded as null without a discriminator.
func (e Embedding[TVector]) MarshalJSON() ([]byte, error) {
	var kind string
	switch any(e.Vector).(type) {
	case nil:
	case VectorOf[uint8]:
		kind = "uint8"
	case VectorOf[int8]:
		kind = "int8"
	case VectorOf[float32]:
		kind = "float32"
	case VectorOf[float64]:
		kind = "float64"
	case Binary:
		kind = "binary"
	default:
		return nil, fmt.Errorf("embedding: unsupported vector type %T", e.Vector)
	}
	type plain Embedding[TVector]
	return json.Marshal(struct {
		Type string `json:",omitzero"`
		plain
	}{kind, plain(e)})
}

// UnmarshalJSON decodes an embedding using its Type discriminator. Unknown
// types, vectors incompatible with TVector, and invalid vectors return an error
// without changing the receiver. An absent type is valid only for an absent or
// null vector. Numeric vectors may be null, but individual coordinates must not
// be null. JSON null leaves the receiver unchanged; other valid payloads replace
// it.
func (e *Embedding[TVector]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	type plain Embedding[TVector]
	var result Embedding[TVector]
	value := struct {
		Type   string
		Vector json.RawMessage
		*plain
	}{plain: (*plain)(&result)}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var vector Vector
	var err error
	switch value.Type {
	case "":
		if len(value.Vector) > 0 && !bytes.Equal(bytes.TrimSpace(value.Vector), []byte("null")) {
			return fmt.Errorf("embedding: vector type is required")
		}
	case "uint8":
		vector, err = unmarshalVector[uint8](value.Vector)
	case "int8":
		vector, err = unmarshalVector[int8](value.Vector)
	case "float32":
		vector, err = unmarshalVector[float32](value.Vector)
	case "float64":
		vector, err = unmarshalVector[float64](value.Vector)
	case "binary":
		var bits Binary
		err = json.Unmarshal(value.Vector, &bits)
		vector = bits
	default:
		return fmt.Errorf("embedding: unsupported vector type %q", value.Type)
	}
	if err != nil {
		return err
	}
	if vector != nil {
		var ok bool
		result.Vector, ok = vector.(TVector)
		if !ok {
			return fmt.Errorf("embedding: expected vector type %T, got %T", result.Vector, vector)
		}
	}
	*e = result
	return nil
}

// VectorOf is a numeric vector with one T per dimension. The slice is borrowed,
// not copied; modifying its elements modifies any embedding that uses it.
// Its zero value is empty. Embedding JSON supports uint8, int8, float32, and
// float64 elements. Other element types can be used in memory.
type VectorOf[T any] []T

// Dimensions returns the number of elements in the vector.
func (v VectorOf[T]) Dimensions() int { return len(v) }

func unmarshalVector[T any](data []byte) (Vector, error) {
	data = bytes.TrimSpace(data)
	var vector VectorOf[T]
	if len(data) == 0 || data[0] != '[' {
		if err := json.Unmarshal(data, &vector); err != nil {
			return nil, err
		}
		return vector, nil
	}
	var elements []*T
	if err := json.Unmarshal(data, &elements); err != nil {
		return nil, err
	}
	vector = make(VectorOf[T], len(elements))
	for i, value := range elements {
		if value == nil {
			return nil, fmt.Errorf("embedding: numeric vector contains a null element at index %d", i)
		}
		vector[i] = *value
	}
	return vector, nil
}

// Binary is a bit vector with one bool per dimension. The slice is borrowed,
// not copied. Its zero value is empty. JSON encodes it as a string of '0' and
// '1' characters.
type Binary []bool

// Dimensions returns the number of bits in the vector.
func (v Binary) Dimensions() int { return len(v) }

// MarshalJSON encodes the bits as a binary string.
func (v Binary) MarshalJSON() ([]byte, error) {
	bits := make([]byte, len(v))
	for i, bit := range v {
		bits[i] = '0'
		if bit {
			bits[i] = '1'
		}
	}
	return json.Marshal(string(bits))
}

// UnmarshalJSON decodes a binary string. Invalid bits or non-string values
// return an error without changing the receiver.
func (v *Binary) UnmarshalJSON(data []byte) error {
	var value *string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == nil {
		return fmt.Errorf("embedding: binary vector must be a string")
	}
	bits := make(Binary, len(*value))
	for i := range *value {
		switch (*value)[i] {
		case '0':
		case '1':
			bits[i] = true
		default:
			return fmt.Errorf("embedding: invalid binary character at index %d", i)
		}
	}
	*v = bits
	return nil
}

// Generated contains an ordered batch of embeddings and generation metadata.
// Use ordinary slice operations to inspect and modify Embeddings. The zero
// value represents an empty result. Usage is nil when no usage was reported.
type Generated[TEmbedding any] struct {
	Embeddings           []TEmbedding
	Usage                *message.UsageDetails `json:",omitzero"`
	AdditionalProperties map[string]any        `json:",omitzero"`
}

// GeneratedOf is an alias for Generated[*Of[T]], a batch of pointers to numeric
// embeddings with vector elements of type T.
type GeneratedOf[T any] = Generated[*Of[T]]
