// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"reflect"

	"github.com/google/uuid"
)

// RequestPort contains information about an input port, including its input and output types.
type RequestPort struct {
	ID           string
	RequestType  reflect.Type
	ResponseType reflect.Type
}

// ExternalResponse represents a response to an external request.
type ExternalResponse struct {
	RequestID   string
	RequestPort RequestPort
	Data        Value
}

type ExternalRequest struct {
	ID          string
	RequestPort RequestPort
	Data        Value
}

func NewExternalRequest(id string, port RequestPort, data any) (*ExternalRequest, error) {
	if id == "" {
		id = uuid.New().String()
	}
	if typ := reflect.TypeOf(data); typ != port.RequestType {
		return nil, fmt.Errorf("invalid request data type: expected %v, got %v", port.RequestType, typ)
	}
	return &ExternalRequest{
		ID:          id,
		RequestPort: port,
		Data:        AnyValue(data),
	}, nil
}

func (r *ExternalRequest) NewResponse(data any) (ExternalResponse, error) {
	if typ := reflect.TypeOf(data); typ != r.RequestPort.ResponseType {
		return ExternalResponse{}, fmt.Errorf("invalid response type: expected %v, got %v", r.RequestPort.ResponseType, typ)
	}
	return ExternalResponse{
		RequestID:   r.ID,
		RequestPort: r.RequestPort,
		Data:        AnyValue(data),
	}, nil
}

func (r *ExternalRequest) Rewrap(other *ExternalResponse) *ExternalResponse {
	return &ExternalResponse{
		RequestID:   other.RequestID,
		RequestPort: other.RequestPort,
		Data:        r.Data,
	}
}
