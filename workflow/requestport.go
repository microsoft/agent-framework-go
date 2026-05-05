// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"reflect"

	"github.com/google/uuid"
)

// RequestPort contains information about an input port, including its input and output types.
type RequestPort struct {
	ID       string
	Request  reflect.Type
	Response reflect.Type
}

// ExternalResponse represents a response to an external request.
type ExternalResponse struct {
	PortInfo  RequestPortInfo
	RequestID string
	Data      PortableValue
}

type ExternalRequest struct {
	PortInfo RequestPortInfo
	ID       string
	Data     PortableValue
}

func NewExternalRequest(id string, port RequestPort, data any) (*ExternalRequest, error) {
	if id == "" {
		id = uuid.New().String()
	}
	if !valueAssignableTo(data, port.Request) {
		typ := reflect.TypeOf(data)
		return nil, fmt.Errorf("invalid request data type: expected %v, got %v", port.Request, typ)
	}
	return &ExternalRequest{
		PortInfo: NewRequestPortInfo(port),
		ID:       id,
		Data:     AnyPortableValue(data),
	}, nil
}

func (r *ExternalRequest) NewResponse(data any) (*ExternalResponse, error) {
	if typ := reflect.TypeOf(data); typ == nil || !r.PortInfo.ResponseType.Match(typ) {
		return nil, fmt.Errorf("invalid response type: expected %v, got %v", r.PortInfo.ResponseType, typ)
	}
	return &ExternalResponse{
		PortInfo:  r.PortInfo,
		RequestID: r.ID,
		Data:      AnyPortableValue(data),
	}, nil
}

func valueAssignableTo(data any, typ reflect.Type) bool {
	if data == nil || typ == nil {
		return false
	}
	if value, ok := data.(PortableValue); ok {
		return value.Is(typ)
	}
	if value, ok := data.(*PortableValue); ok {
		return value.Is(typ)
	}
	return reflect.TypeOf(data).AssignableTo(typ)
}
