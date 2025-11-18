// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/microsoft/agent-framework/go/internal/jsonx"
)

var supportedContents map[contentKind]reflect.Type

func init() {
	supportedContents = make(map[contentKind]reflect.Type)
	for _, c := range []Content{
		&TextContent{},
		&DataContent{},
		&ErrorContent{},
		&FunctionCallContent{},
		&FunctionResultContent{},
		&HostedFileContent{},
		&HostedVectorStoreContent{},
		&TextReasoningContent{},
		&URIContent{},
		&UsageContent{},
		&FunctionApprovalRequestContent{},
		&FunctionApprovalResponseContent{},
	} {
		supportedContents[c.kind()] = reflect.TypeOf(c).Elem()
	}
}

// contentKind represents the type of content.
// It is unexported to prevent external implementations of Content.
type contentKind string

// Content represents message content.
type Content interface {
	json.Marshaler
	kind() contentKind
}

// Contents is a slice of Content that supports JSON encoding.
type Contents []Content

func (cs *Contents) UnmarshalJSON(data []byte) error {
	var err error
	*cs, err = jsonx.UnmarshalDiscriminatedUnionSlice[Content](data, supportedContents)
	return err
}

// TextContent represents plain text content.
type TextContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	Text string
}

func (t *TextContent) MarshalJSON() ([]byte, error) {
	type alias TextContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t TextContent) kind() contentKind { return "text" }

// String returns the text of the content.
func (t *TextContent) String() string { return t.Text }

// DataContent represents binary content with an associated media type.
//
// The content represents in-memory data. For references to data at a remote URI,
// use [URIContent] instead.
type DataContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	Data      []byte
	MediaType string
	Name      string `json:",omitempty"`
	URI       string `json:",omitempty"`
}

func (t *DataContent) MarshalJSON() ([]byte, error) {
	type alias DataContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t DataContent) kind() contentKind { return "data" }

// ErrorContent represents an error.
//
// Typically, ErrorContent is used for non-fatal errors, where something went wrong as part
// of the operation but the operation was still able to continue.
type ErrorContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	Message   string
	Details   string `json:",omitempty"`
	ErrorCode string `json:",omitempty"`
}

func (t *ErrorContent) MarshalJSON() ([]byte, error) {
	type alias ErrorContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t ErrorContent) kind() contentKind { return "error" }

type serializedFunctionCallContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	Arguments string
	CallID    string
	Error     string `json:",omitempty"`
	Name      string `json:",omitempty"`

	Type contentKind
}

// FunctionCallContent represents a function call request.
type FunctionCallContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	Arguments string // Arguments as a JSON-encoded string.
	CallID    string
	Error     error // Error that occurred while mapping the original function call data to this object.
	Name      string
}

func (t *FunctionCallContent) MarshalJSON() ([]byte, error) {
	tmp := serializedFunctionCallContent{
		AdditionalProperties: t.AdditionalProperties,
		Annotations:          t.Annotations,
		RawRepresentation:    t.RawRepresentation,
		Arguments:            t.Arguments,
		CallID:               t.CallID,
		Name:                 t.Name,
		Type:                 t.kind(),
	}
	if t.Error != nil {
		tmp.Error = t.Error.Error()
	}
	return json.Marshal(tmp)
}

func (t *FunctionCallContent) UnmarshalJSON(data []byte) error {
	var tmp serializedFunctionCallContent
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	t.AdditionalProperties = tmp.AdditionalProperties
	t.Annotations = tmp.Annotations
	t.RawRepresentation = tmp.RawRepresentation
	t.Arguments = tmp.Arguments
	t.CallID = tmp.CallID
	t.Name = tmp.Name
	if tmp.Error != "" {
		t.Error = errors.New(tmp.Error)
	}
	return nil
}

func (t *FunctionCallContent) ParseArgs() (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(t.Arguments), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func (t FunctionCallContent) kind() contentKind { return "function_call" }

type serializedFunctionResultContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	CallID string
	Error  string `json:",omitempty"`
	Result any    `json:",omitempty"`

	Type contentKind
}

// FunctionResultContent represents the result of a function call.
type FunctionResultContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	CallID string
	Error  error `json:",omitempty"` // Error that occurred if the function call failed.
	Result any   `json:",omitempty"`
}

func (t *FunctionResultContent) MarshalJSON() ([]byte, error) {
	tmp := serializedFunctionResultContent{
		AdditionalProperties: t.AdditionalProperties,
		Annotations:          t.Annotations,
		RawRepresentation:    t.RawRepresentation,
		CallID:               t.CallID,
		Result:               t.Result,
		Type:                 t.kind(),
	}
	if t.Error != nil {
		tmp.Error = t.Error.Error()
	}
	return json.Marshal(tmp)
}

func (t *FunctionResultContent) UnmarshalJSON(data []byte) error {
	var tmp serializedFunctionResultContent
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	t.AdditionalProperties = tmp.AdditionalProperties
	t.Annotations = tmp.Annotations
	t.RawRepresentation = tmp.RawRepresentation
	t.CallID = tmp.CallID
	t.Result = tmp.Result
	if tmp.Error != "" {
		t.Error = errors.New(tmp.Error)
	}
	return nil
}

func (t FunctionResultContent) kind() contentKind { return "function_result" }

// HostedFileContent represents a file that is hosted by the AI service.
//
// Unlike [DataContent] which contains the data for a file or blob, this class represents a file
// that is hosted by the AI service and referenced by an identifier.
// Such identifiers are specific to the provider.
type HostedFileContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	FileID string
}

func (t *HostedFileContent) MarshalJSON() ([]byte, error) {
	type alias HostedFileContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t HostedFileContent) kind() contentKind { return "hosted_file" }

// HostedVectorStoreContent represents a vector store that is hosted by the AI service.
//
// Unlike [HostedFileContent] which represents a specific file that is hosted by the AI service,
// HostedVectorStoreContent represents a vector store that can contain multiple files, indexed for searching.
type HostedVectorStoreContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	VectorStoreID string
}

func (t *HostedVectorStoreContent) MarshalJSON() ([]byte, error) {
	type alias HostedVectorStoreContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t HostedVectorStoreContent) kind() contentKind { return "hosted_vector_store" }

// TextReasoningContent represents text reasoning content in a chat.
//
// TextReasoningContent is distinct from [TextContent]. TextReasoningContent represents "thinking" or "reasoning"
// performed by the model and is distinct from the actual output text from the model,
// which is represented by [TextContent].
type TextReasoningContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	ProtectedData string `json:",omitempty"`
	Text          string
}

func (t *TextReasoningContent) MarshalJSON() ([]byte, error) {
	type alias TextReasoningContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t TextReasoningContent) kind() contentKind { return "text_reasoning" }

// String returns the text of the reasoning content.
func (t *TextReasoningContent) String() string { return t.Text }

// URIContent represents a URL, typically to hosted content such as an image, audio, or video.
type URIContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	MediaType string
	URI       string
}

func (t *URIContent) MarshalJSON() ([]byte, error) {
	type alias URIContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t URIContent) kind() contentKind { return "uri" }

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

// UsageContent represents usage information associated with a chat request and response.
type UsageContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	Details UsageDetails
}

func (t *UsageContent) MarshalJSON() ([]byte, error) {
	type alias UsageContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t UsageContent) kind() contentKind { return "usage" }

type FunctionApprovalRequestContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	ID           string
	FunctionCall *FunctionCallContent
}

func (t *FunctionApprovalRequestContent) MarshalJSON() ([]byte, error) {
	type alias FunctionApprovalRequestContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t FunctionApprovalRequestContent) kind() contentKind { return "functionApprovalRequest" }

func (t *FunctionApprovalRequestContent) Response(approved bool) *FunctionApprovalResponseContent {
	return &FunctionApprovalResponseContent{
		ID:                   t.ID,
		Approved:             approved,
		FunctionCall:         t.FunctionCall,
		AdditionalProperties: t.AdditionalProperties,
	}
}

type FunctionApprovalResponseContent struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`

	ID           string
	Approved     bool
	FunctionCall *FunctionCallContent
}

func (t *FunctionApprovalResponseContent) MarshalJSON() ([]byte, error) {
	type alias FunctionApprovalResponseContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t FunctionApprovalResponseContent) kind() contentKind { return "functionApprovalResponse" }
