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
		{
			name:    "malformed percent escape",
			uri:     "data:text/plain,%ZZ",
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

func TestParseDataURIPercentEncodedBase64(t *testing.T) {
	// RFC 2397 defines the payload as *urlchar: a base64 payload may be
	// percent-encoded (encoders commonly escape '=', '+' and '/'). The WHATWG
	// data: URL processor percent-decodes before base64-decoding; so must we.
	for _, tc := range []struct {
		uri  string
		want string
	}{
		{"data:text/plain;base64,aGVsbG8%3D", "hello"},    // '=' escaped
		{"data:text/plain;base64,YSs%2FYg%3D%3D", "a+?b"}, // '/' and '==' escaped
		{"data:text/plain;base64,aGVsbG8=", "hello"},      // unescaped still works
	} {
		data, mediaType, err := message.DecodeDataURI(tc.uri)
		if err != nil {
			t.Fatalf("DecodeDataURI(%q) error = %v", tc.uri, err)
		}
		if string(data) != tc.want {
			t.Errorf("DecodeDataURI(%q) = %q, want %q", tc.uri, data, tc.want)
		}
		if mediaType != "text/plain" {
			t.Errorf("DecodeDataURI(%q) mediaType = %q, want text/plain", tc.uri, mediaType)
		}
	}

	// NewDataContentFromURI goes through the same parser and must round-trip.
	content, err := message.NewDataContentFromURI("data:text/plain;base64,aGVsbG8%3D", "")
	if err != nil {
		t.Fatalf("NewDataContentFromURI error = %v", err)
	}
	decoded, err := content.Bytes()
	if err != nil {
		t.Fatalf("Bytes() error = %v", err)
	}
	if string(decoded) != "hello" {
		t.Errorf("Bytes() = %q, want %q", decoded, "hello")
	}

	// Invalid percent-encoding in a base64 payload is an error, not silence.
	if _, _, err := message.DecodeDataURI("data:text/plain;base64,aGVs%GGbG8="); err == nil {
		t.Error("DecodeDataURI with invalid percent-encoding: expected error, got nil")
	}
}
