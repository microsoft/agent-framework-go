// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestDecodeDataURI(t *testing.T) {
	tests := []struct {
		name          string
		uri           string
		wantData      string
		wantMediaType string
		wantErr       bool
	}{
		{
			name:          "percent-encoded payload",
			uri:           "data:text/plain,hello%20world",
			wantData:      "hello world",
			wantMediaType: "text/plain",
		},
		{
			name:          "percent-encoded default media type",
			uri:           "data:,hello%2Cworld",
			wantData:      "hello,world",
			wantMediaType: "text/plain;charset=US-ASCII",
		},
		{
			name:          "base64 payload",
			uri:           "data:text/plain;base64,aGVsbG8gd29ybGQ=",
			wantData:      "hello world",
			wantMediaType: "text/plain",
		},
		{
			name:    "missing scheme",
			uri:     "text/plain,hello",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, mediaType, err := message.DecodeDataURI(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodeDataURI(%q) = nil error, want error", tt.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeDataURI(%q) unexpected error: %v", tt.uri, err)
			}
			if string(data) != tt.wantData {
				t.Errorf("data = %q, want %q", string(data), tt.wantData)
			}
			if mediaType != tt.wantMediaType {
				t.Errorf("mediaType = %q, want %q", mediaType, tt.wantMediaType)
			}
		})
	}
}
