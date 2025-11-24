// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"encoding/base64"
	"fmt"
	"mime"
	"strings"
)

const dataURIScheme = "data:"

// dataURI represents a parsed data URI with its components.
// Based on RFC 2397: https://datatracker.ietf.org/doc/html/rfc2397
type dataURI struct {
	Data      string
	IsBase64  bool
	MediaType string
}

// parseDataURI parses a data URI string and returns a DataURI struct.
// It validates the format and extracts the media type, encoding, and data.
func parseDataURI(uri string) (*dataURI, error) {
	// Validate and trim off the "data:" scheme
	if !strings.HasPrefix(strings.ToLower(uri), dataURIScheme) {
		return nil, fmt.Errorf("invalid data URI format: the data URI must start with 'data:'")
	}

	uri = uri[len(dataURIScheme):]

	// Find the comma separating the metadata from the data
	commaPos := strings.IndexByte(uri, ',')
	if commaPos < 0 {
		return nil, fmt.Errorf("invalid data URI format: the data URI must contain a comma separating the metadata and the data")
	}

	metadata := uri[:commaPos]
	data := uri[commaPos+1:]
	isBase64 := false

	// Determine whether the data is Base64-encoded or percent-encoded (URL-encoded)
	if strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		metadata = metadata[:len(metadata)-len(";base64")]
		isBase64 = true
		if !isValidBase64Data(data) {
			return nil, fmt.Errorf("invalid data URI format: the data URI is base64-encoded, but the data is not a valid base64 string")
		}
	}

	// Validate the media type, if present
	mediaType := strings.TrimSpace(metadata)
	if mediaType != "" && !isValidMediaType(mediaType) {
		return nil, fmt.Errorf("invalid data URI format: the media type is not valid")
	}
	return &dataURI{
		Data:      data,
		IsBase64:  isBase64,
		MediaType: strings.ToLower(mediaType),
	}, nil
}

// data returns the raw data portion of the data URI as a base64-encoded string.
func (d *dataURI) data() string {
	if d.IsBase64 {
		return d.Data
	}
	return base64.StdEncoding.EncodeToString([]byte(d.Data))
}

// isValidMediaType validates that a media type is valid.
func isValidMediaType(mediaType string) bool {
	if mediaType == "" {
		return false
	}

	// Check for common known media types for fast path
	switch mediaType {
	case "application/json", "application/octet-stream", "application/pdf", "application/xml",
		"audio/mpeg", "audio/ogg", "audio/wav",
		"image/apng", "image/avif", "image/bmp", "image/gif", "image/jpeg", "image/png",
		"image/svg+xml", "image/tiff", "image/webp",
		"text/css", "text/csv", "text/html", "text/javascript", "text/plain",
		"text/plain;charset=UTF-8", "text/xml":
		return true
	}

	// Use mime package to parse and validate the media type
	_, _, err := mime.ParseMediaType(mediaType)
	return err == nil
}

// topLevelMediaType returns the top-level type of the given media type.
func topLevelMediaType(mediaType string) string {
	if mediaType == "" {
		return ""
	}
	slashIndex := strings.IndexByte(mediaType, '/')
	var topLevel string
	if slashIndex < 0 {
		topLevel = mediaType
	} else {
		topLevel = mediaType[:slashIndex]
	}
	return strings.ToLower(strings.TrimSpace(topLevel))
}

// isValidBase64Data tests whether the value is a valid base64 string without whitespace.
func isValidBase64Data(value string) bool {
	if value == "" {
		return true
	}

	// Check length is multiple of 4
	if len(value)%4 != 0 {
		return false
	}

	// Check for whitespace
	if strings.ContainsAny(value, " \t\r\n") {
		return false
	}

	index := len(value) - 1

	// Step back over one or two padding chars
	if value[index] == '=' {
		index--
	}
	if index >= 0 && value[index] == '=' {
		index--
	}

	// Now traverse over characters
	for i := 0; i <= index; i++ {
		c := value[i]
		validChar := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/'
		if !validChar {
			return false
		}
	}

	return true
}
