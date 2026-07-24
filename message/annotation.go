// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"encoding/json"
	"reflect"

	"github.com/microsoft/agent-framework-go/internal/jsonx"
)

var (
	supportedAnnotations      map[annotationKind]reflect.Type
	supportedAnnotatedRegions map[annotatedRegionKind]reflect.Type
)

func init() {
	supportedAnnotations = make(map[annotationKind]reflect.Type)
	for _, a := range []Annotation{
		&CitationAnnotation{},
	} {
		supportedAnnotations[a.kind()] = reflect.TypeOf(a).Elem()
	}

	supportedAnnotatedRegions = make(map[annotatedRegionKind]reflect.Type)
	for _, ar := range []AnnotatedRegion{
		&TextSpanAnnotatedRegion{},
	} {
		supportedAnnotatedRegions[ar.kind()] = reflect.TypeOf(ar).Elem()
	}
}

// annotationKind represents the type of annotation.
// It is unexported to prevent external implementations of Annotation.
type annotationKind string

// Annotations is a slice of [Annotation] values; its UnmarshalJSON decodes each
// element as a discriminated union based on its type discriminator (currently
// [CitationAnnotation]).
type Annotations []Annotation

func (as *Annotations) UnmarshalJSON(data []byte) error {
	var err error
	*as, err = jsonx.UnmarshalDiscriminatedUnionSlice[Annotation](data, supportedAnnotations)
	return err
}

// Annotation represents an annotation on content.
type Annotation interface {
	kind() annotationKind
}

// CitationAnnotation represents an annotation that links content to source references,
// such as documents, URLs, files, or tool outputs.
type CitationAnnotation struct {
	AdditionalProperties map[string]any   `json:"-"`
	AnnotatedRegions     AnnotatedRegions `json:",omitempty"`
	RawRepresentation    any              `json:"-"`

	FileID   string `json:",omitempty"` // Source identifier associated with the annotation.
	Snippet  string `json:",omitempty"` // Snippet or excerpt from the source that was cited.
	Title    string `json:",omitempty"` // Title or name of the source.
	ToolName string `json:",omitempty"` // Name of any tool involved in the production of the associated content.
	URL      string `json:",omitempty"` // URI from which the source material was retrieved.
}

func (t *CitationAnnotation) MarshalJSON() ([]byte, error) {
	type alias CitationAnnotation
	tmp := struct {
		*alias
		Type annotationKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t *CitationAnnotation) kind() annotationKind { return "citation" }

type annotatedRegionKind string

// AnnotatedRegions is a slice of [AnnotatedRegion] values; its UnmarshalJSON
// decodes each element as a discriminated union based on its type discriminator
// (currently [TextSpanAnnotatedRegion]).
type AnnotatedRegions []AnnotatedRegion

func (as *AnnotatedRegions) UnmarshalJSON(data []byte) error {
	var err error
	*as, err = jsonx.UnmarshalDiscriminatedUnionSlice[AnnotatedRegion](data, supportedAnnotatedRegions)
	return err
}

// AnnotatedRegion describes the portion of an associated [Content]
// to which an annotation applies.
type AnnotatedRegion interface {
	kind() annotatedRegionKind
}

// TextSpanAnnotatedRegion describes a location in the associated [Content]
// based on starting and ending character indices.
type TextSpanAnnotatedRegion struct {
	Start int // Start character index (inclusive) of the annotated span.
	End   int // End character index (exclusive) of the annotated span.
}

func (t *TextSpanAnnotatedRegion) MarshalJSON() ([]byte, error) {
	type alias TextSpanAnnotatedRegion
	tmp := struct {
		*alias
		Type annotatedRegionKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t *TextSpanAnnotatedRegion) kind() annotatedRegionKind { return "text_span" }
