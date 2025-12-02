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
	RequestID   string
	RequestPort RequestPort
	Data        PortableValue
}

type ExternalRequest struct {
	ID          string
	RequestPort RequestPort
	Data        PortableValue
}

func NewExternalRequest(id string, port RequestPort, data any) (*ExternalRequest, error) {
	if id == "" {
		id = uuid.New().String()
	}
	if typ := reflect.TypeOf(data); typ != port.Request {
		return nil, fmt.Errorf("invalid request data type: expected %v, got %v", port.Request, typ)
	}
	return &ExternalRequest{
		ID:          id,
		RequestPort: port,
		Data:        AnyPortableValue(data),
	}, nil
}

func (r *ExternalRequest) NewResponse(data any) (*ExternalResponse, error) {
	if typ := reflect.TypeOf(data); typ != r.RequestPort.Response {
		return nil, fmt.Errorf("invalid response type: expected %v, got %v", r.RequestPort.Response, typ)
	}
	return &ExternalResponse{
		RequestID:   r.ID,
		RequestPort: r.RequestPort,
		Data:        AnyPortableValue(data),
	}, nil
}

func (r *ExternalRequest) Rewrap(other *ExternalResponse) *ExternalResponse {
	return &ExternalResponse{
		RequestID:   other.RequestID,
		RequestPort: other.RequestPort,
		Data:        r.Data,
	}
}
