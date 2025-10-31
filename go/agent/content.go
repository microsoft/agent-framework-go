// Copyright (c) Microsoft. All rights reserved.

package agent

import "encoding/json"

// AnnotatedRegion describes the portion of an associated [Content]
// to which an annotation applies.
type AnnotatedRegion interface {
	isRegion()
}

// TextSpanAnnotatedRegion describes a location in the associated [Content]
// based on starting and ending character indices.
type TextSpanAnnotatedRegion struct {
	Start int // Start character index (inclusive) of the annotated span.
	End   int // End character index (exclusive) of the annotated span.
}

func (t *TextSpanAnnotatedRegion) isRegion() {}

// Annotation represents an annotation on content.
type Annotation interface {
	isAnnotation()
}

// CitationAnnotation represents an annotation that links content to source references,
// such as documents, URLs, files, or tool outputs.
type CitationAnnotation struct {
	AdditionalProperties map[string]any
	AnnotatedRegions     []AnnotatedRegion
	RawRepresentation    any

	FileID   string // Source identifier associated with the annotation.
	Snippet  string // Snippet or excerpt from the source that was cited.
	Title    string // Title or name of the source.
	ToolName string // Name of any tool involved in the production of the associated content.
	URL      string // URI from which the source material was retrieved.
}

func (t *CitationAnnotation) isAnnotation() {}

// Content represents message content.
type Content interface {
	isContent()
}

// TextContent represents plain text content.
type TextContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Text string
}

func (t *TextContent) isContent() {}

// String returns the text of the content.
func (t *TextContent) String() string { return t.Text }

// DataContent represents binary content with an associated media type.
//
// The content represents in-memory data. For references to data at a remote URI,
// use [URIContent] instead.
type DataContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Data      []byte
	MediaType string
	Name      string
	URI       string
}

func (t *DataContent) isContent() {}

// ErrorContent represents an error.
//
// Typically, ErrorContent is used for non-fatal errors, where something went wrong as part
// of the operation but the operation was still able to continue.
type ErrorContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Details   string
	ErrorCode string
	Message   string
}

func (t *ErrorContent) isContent() {}

// FunctionCallContent represents a function call request.
type FunctionCallContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Arguments string // Arguments as a JSON-encoded string.
	CallID    string
	Error     error // Error that occurred while mapping the original function call data to this object.
	Name      string
}

func (t *FunctionCallContent) ParseArgs() (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(t.Arguments), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func (t *FunctionCallContent) isContent() {}

// FunctionResultContent represents the result of a function call.
type FunctionResultContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	CallID string
	Error  error // Error that occurred if the function call failed.
	Result any
}

func (t *FunctionResultContent) isContent() {}

// HostedFileContent represents a file that is hosted by the AI service.
//
// Unlike [DataContent] which contains the data for a file or blob, this class represents a file
// that is hosted by the AI service and referenced by an identifier.
// Such identifiers are specific to the provider.
type HostedFileContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	FileID string
}

func (t *HostedFileContent) isContent() {}

// HostedVectorStoreContent represents a vector store that is hosted by the AI service.
//
// Unlike [HostedFileContent] which represents a specific file that is hosted by the AI service,
// HostedVectorStoreContent represents a vector store that can contain multiple files, indexed for searching.
type HostedVectorStoreContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	VectorStoreID string
}

func (t *HostedVectorStoreContent) isContent() {}

// TextReasoningContent represents text reasoning content in a chat.
//
// TextReasoningContent is distinct from [TextContent]. TextReasoningContent represents "thinking" or "reasoning"
// performed by the model and is distinct from the actual output text from the model,
// which is represented by [TextContent].
type TextReasoningContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	ProtectedData string // Opaque blob of data associated with this reasoning content.
	Text          string
}

func (t *TextReasoningContent) isContent() {}

// String returns the text of the reasoning content.
func (t *TextReasoningContent) String() string { return t.Text }

// URIContent represents a URL, typically to hosted content such as an image, audio, or video.
type URIContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	MediaType string
	URI       string
}

func (t *URIContent) isContent() {}

// UsageContent represents usage information associated with a chat request and response.
type UsageContent struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Details UsageDetails
}

func (t *UsageContent) isContent() {}
