// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
)

// NewDataContentFromFile loads a [DataContent] from path. When mediaType is
// empty, it is inferred from the file extension or defaults to
// application/octet-stream.
func NewDataContentFromFile(path string, mediaType string) (*DataContent, error) {
	if path == "" {
		return nil, fmt.Errorf("path cannot be empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	content, err := NewDataContentFromReader(file, mediaType)
	if err != nil {
		return nil, err
	}
	content.Name = filepath.Base(path)
	return content, nil
}

// NewDataContentFromReader reads all data from reader. When reader is an
// [os.File], its name is used for the content name and media-type inference.
// Other readers default to application/octet-stream when mediaType is empty.
func NewDataContentFromReader(reader io.Reader, mediaType string) (*DataContent, error) {
	if reader == nil {
		return nil, fmt.Errorf("reader cannot be nil")
	}

	var name string
	if file, ok := reader.(*os.File); ok {
		name = filepath.Base(file.Name())
		if mediaType == "" {
			mediaType = inferMediaTypeFromFilePath(file.Name())
		}
	}
	if mediaType == "" {
		mediaType = uriContentDefaultMediaType
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	content, err := NewDataContent(data, mediaType)
	if err != nil {
		return nil, err
	}
	content.Name = name
	return content, nil
}

// SaveToFile writes the decoded content to path without overwriting an
// existing file. If path is empty or names an existing directory, Name is used
// as the file name; otherwise a random name and inferred extension are used.
// The returned path identifies the created file.
func (t *DataContent) SaveToFile(path string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("content cannot be nil")
	}
	data, err := t.Bytes()
	if err != nil {
		return "", err
	}

	directory := ""
	if path == "" {
		directory = "."
	} else if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		directory = path
	}

	if directory != "" {
		if name := filepath.Base(t.Name); t.Name != "" && name != "." {
			path = filepath.Join(directory, name)
		} else {
			file, createErr := os.CreateTemp(directory, "*"+extensionForMediaType(t.MediaType))
			if createErr != nil {
				return "", createErr
			}
			return writeDataContentFile(file, data)
		}
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return "", err
	}
	return writeDataContentFile(file, data)
}

func writeDataContentFile(file *os.File, data []byte) (path string, err error) {
	filePath := file.Name()
	path = filePath
	defer func() {
		if err != nil {
			_ = file.Close()
			_ = os.Remove(filePath)
		}
	}()
	if _, err = file.Write(data); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func inferMediaTypeFromFilePath(path string) string {
	mediaType := mime.TypeByExtension(filepath.Ext(path))
	if mediaType == "" {
		return uriContentDefaultMediaType
	}
	if parsed, _, err := mime.ParseMediaType(mediaType); err == nil {
		return parsed
	}
	return mediaType
}

func extensionForMediaType(mediaType string) string {
	extensions, err := mime.ExtensionsByType(mediaType)
	if err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	return ""
}
