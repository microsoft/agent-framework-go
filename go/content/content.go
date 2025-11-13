// Copyright (c) Microsoft. All rights reserved.

package content

import "encoding/json"

// Content represents message content.
type Content interface {
	isContent()
}

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

// Text represents plain text content.
type Text struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Text string
}

func (t *Text) isContent() {}

// String returns the text of the content.
func (t *Text) String() string { return t.Text }

// Data represents binary content with an associated media type.
//
// The content represents in-memory data. For references to data at a remote URI,
// use [URI] instead.
type Data struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Data      []byte
	MediaType string
	Name      string
	URI       string
}

func (t *Data) isContent() {}

// Error represents an error.
//
// Typically, Error is used for non-fatal errors, where something went wrong as part
// of the operation but the operation was still able to continue.
type Error struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Details   string
	ErrorCode string
	Message   string
}

func (t *Error) isContent() {}

// FunctionCall represents a function call request.
type FunctionCall struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Arguments string // Arguments as a JSON-encoded string.
	CallID    string
	Error     error // Error that occurred while mapping the original function call data to this object.
	Name      string
}

func (t *FunctionCall) ParseArgs() (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(t.Arguments), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func (t *FunctionCall) isContent() {}

// FunctionResult represents the result of a function call.
type FunctionResult struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	CallID string
	Error  error // Error that occurred if the function call failed.
	Result any
}

func (t *FunctionResult) isContent() {}

// HostedFile represents a file that is hosted by the AI service.
//
// Unlike [Data] which contains the data for a file or blob, this class represents a file
// that is hosted by the AI service and referenced by an identifier.
// Such identifiers are specific to the provider.
type HostedFile struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	FileID string
}

func (t *HostedFile) isContent() {}

// HostedVectorStore represents a vector store that is hosted by the AI service.
//
// Unlike [HostedFile] which represents a specific file that is hosted by the AI service,
// HostedVectorStore represents a vector store that can contain multiple files, indexed for searching.
type HostedVectorStore struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	VectorStoreID string
}

func (t *HostedVectorStore) isContent() {}

// TextReasoning represents text reasoning content in a chat.
//
// TextReasoning is distinct from [Text]. TextReasoning represents "thinking" or "reasoning"
// performed by the model and is distinct from the actual output text from the model,
// which is represented by [Text].
type TextReasoning struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	ProtectedData string // Opaque blob of data associated with this reasoning content.
	Text          string
}

func (t *TextReasoning) isContent() {}

// String returns the text of the reasoning content.
func (t *TextReasoning) String() string { return t.Text }

// URI represents a URL, typically to hosted content such as an image, audio, or video.
type URI struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	MediaType string
	URI       string
}

func (t *URI) isContent() {}

// UsageDetails provides usage details about a request/response.
type UsageDetails struct {
	AdditionalCounts map[string]int64
	InputTokenCount  int64
	OutputTokenCount int64
	TotalTokenCount  int64
}

func (u *UsageDetails) Add(other *UsageDetails) {
	if u == nil || other == nil {
		return
	}
	u.InputTokenCount += other.InputTokenCount
	u.OutputTokenCount += other.OutputTokenCount
	u.TotalTokenCount += other.TotalTokenCount

	// Merge additional counts
	if other.AdditionalCounts != nil {
		if u.AdditionalCounts == nil {
			u.AdditionalCounts = make(map[string]int64)
		}
		for k, v := range other.AdditionalCounts {
			u.AdditionalCounts[k] += v
		}
	}
}

// Usage represents usage information associated with a chat request and response.
type Usage struct {
	AdditionalProperties map[string]any
	Annotations          []Annotation
	RawRepresentation    any

	Details UsageDetails
}

func (t *Usage) isContent() {}
