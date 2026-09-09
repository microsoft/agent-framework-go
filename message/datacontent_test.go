// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestNewDataContentFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	want := []byte(`{"value":42}`)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	content, err := message.NewDataContentFromFile(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if content.Name != "input.json" || content.MediaType != "application/json" {
		t.Fatalf("content = %#v", content)
	}
	got, err := content.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Bytes() = %q, want %q", got, want)
	}
}

func TestNewDataContentFromReader(t *testing.T) {
	content, err := message.NewDataContentFromReader(bytes.NewBufferString("hello"), "")
	if err != nil {
		t.Fatal(err)
	}
	if content.Name != "" || content.MediaType != "application/octet-stream" {
		t.Fatalf("content = %#v", content)
	}
}

func TestDataContentSaveToFile(t *testing.T) {
	content, err := message.NewDataContent([]byte("hello"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	content.Name = filepath.Join("ignored", "note.txt")

	directory := t.TempDir()
	path, err := content.SaveToFile(directory)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(directory, "note.txt"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("saved data = %q, want hello", data)
	}
}

func TestDataContentSaveToFileDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.bin")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := message.NewDataContent([]byte("replacement"), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.SaveToFile(path); err == nil {
		t.Fatal("SaveToFile() error = nil, want existing-file error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("existing data = %q, want original", data)
	}
}
