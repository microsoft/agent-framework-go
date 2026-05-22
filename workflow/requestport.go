// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"reflect"

	"github.com/google/uuid"
)

// RequestPort is an external request port for a [Workflow] with the specified
// request and response types.
type RequestPort struct {
	// ID is the unique identifier for the input port.
	ID string

	// Request is the type of request messages that the input port will accept.
	Request reflect.Type

	// Response is the type of response messages that the input port will produce.
	Response reflect.Type
}

// ExternalResponse represents a response from an external input port.
type ExternalResponse struct {
	// PortInfo is the port invoked.
	PortInfo RequestPortInfo

	// RequestID is the unique identifier of the corresponding request.
	RequestID string

	// Data is the data contained in the response.
	Data PortableValue
}

// ExternalRequest represents a request to an external input port.
type ExternalRequest struct {
	// PortInfo is the port to invoke.
	PortInfo RequestPortInfo

	// RequestID is a unique identifier for this request instance.
	RequestID string

	// Data is the data contained in the request.
	Data PortableValue
}

// NewExternalRequest creates a new [ExternalRequest] for the specified input
// port and data payload.
//
// id is an optional unique identifier for this request instance. If id is empty,
// a UUID will be generated.
//
// NewExternalRequest returns an error when data does not match the expected
// request type.
func NewExternalRequest(id string, port RequestPort, data any) (*ExternalRequest, error) {
	if id == "" {
		id = uuid.New().String()
	}
	if !valueAssignableTo(data, port.Request) {
		typ := reflect.TypeOf(data)
		return nil, fmt.Errorf("invalid request data type: expected %v, got %v", port.Request, typ)
	}
	return &ExternalRequest{
		PortInfo:  NewRequestPortInfo(port),
		RequestID: id,
		Data:      AnyPortableValue(data),
	}, nil
}

// CreateResponse creates a new [ExternalResponse] corresponding to r, with the
// specified data payload.
//
// CreateResponse returns an error when data does not match the expected response
// type.
func (r *ExternalRequest) CreateResponse(data any) (*ExternalResponse, error) {
	if !valueMatchesTypeID(data, r.PortInfo.ResponseType) {
		return nil, fmt.Errorf("invalid response type: expected %v, got %v", r.PortInfo.ResponseType, reflect.TypeOf(data))
	}
	return &ExternalResponse{
		PortInfo:  r.PortInfo,
		RequestID: r.RequestID,
		Data:      AnyPortableValue(data),
	}, nil
}

// valueAssignableTo reports whether data can be used as a value of typ.
//
// Values use assignability from their concrete runtime type.
func valueAssignableTo(data any, typ reflect.Type) bool {
	if isNilValue(data) || typ == nil {
		return false
	}
	return reflect.TypeOf(data).AssignableTo(typ)
}

func valueMatchesTypeID(data any, typeID TypeID) bool {
	if isNilValue(data) {
		return false
	}
	return typeID.MatchPolymorphic(reflect.TypeOf(data))
}

func isNilValue(data any) bool {
	if data == nil {
		return true
	}
	value := reflect.ValueOf(data)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
