// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLocalAssemblyInventory(t *testing.T) {
	data := testAssembly(t)
	file := filepath.Join(t.TempDir(), "Sample.dll")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if err := run([]string{"-assembly", file, "-namespace", "Example.Child"}, &out, &diagnostics); err != nil {
		t.Fatalf("extract local assembly: %v", err)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %s", diagnostics.String())
	}
	var got inventory
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if got.SchemaVersion != 1 || got.IdentityFormat != "ecma335-v1" || !slices.Equal(got.Selection.Namespaces, []string{"Example.Child"}) {
		t.Fatalf("unexpected inventory metadata: %+v", got)
	}
	assembly, ok := got.Assemblies["Sample"]
	if !ok || assembly.Version != "1.2.3.4" || assembly.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("unexpected assembly: %+v", got.Assemblies)
	}
	if len(got.Types) != 1 || got.Types["Example.Child.Gadget"].Assembly != "Sample" {
		t.Fatalf("namespace filter returned types: %+v", got.Types)
	}

	short := testWriter(func(p []byte) (int, error) { return len(p) / 2, nil })
	if err := run([]string{"-assembly", file}, short, &diagnostics); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short report write: got %v, want io.ErrShortWrite", err)
	}
	writeErr := errors.New("output failed")
	failing := testWriter(func([]byte) (int, error) { return 0, writeErr })
	if err := run([]string{"-assembly", file}, failing, &diagnostics); !errors.Is(err, writeErr) {
		t.Fatalf("failed report write: got %v, want %v", err, writeErr)
	}
}

func TestInvalidCommandInput(t *testing.T) {
	badPE := filepath.Join(t.TempDir(), "bad.dll")
	if err := os.WriteFile(badPE, []byte("not a managed assembly"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"invalid metadata", []string{"-assembly", badPE}, "PE:"},
		{"conflicting modes", []string{"-assembly", badPE, "-release", "1.2.3"}, "cannot be combined"},
		{"missing assembly", []string{"-assembly", filepath.Join(t.TempDir(), "missing.dll")}, "matched no files"},
		{"invalid namespace", []string{"-assembly", badPE, "-namespace", "Example."}, "namespace must be"},
		{"invalid release", []string{"-release", "broken"}, "expected an exact NuGet version"},
		{"invalid package", []string{"-package", "invalid@version"}, "expected an exact NuGet version"},
		{"invalid feed", []string{"-nuget-source", "http://example.test/v3/index.json"}, "expected an absolute HTTPS URL"},
		{"invalid framework", []string{"-framework", "net8.0/other"}, "invalid exact target framework"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			err := run(test.args, &out, &diagnostics)
			if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
				t.Fatalf("run(%v) = %v, output %q; want error containing %q and no report", test.args, err, out.String(), test.want)
			}
		})
	}
	var out, help bytes.Buffer
	if err := run([]string{"-help"}, &out, &help); err != nil || out.Len() != 0 || !strings.Contains(help.String(), "-assembly") {
		t.Fatalf("help: %v, output %q, diagnostics %q", err, out.String(), help.String())
	}
}

func TestLatestStableRelease(t *testing.T) {
	for _, test := range []struct {
		name     string
		versions string
		want     string
		wantErr  string
	}{
		{"numeric order", `{"versions":["1.9.0","1.10.0-rc.1","1.10.0","1.9.9"]}`, "1.10.0", ""},
		{"no stable release", `{"versions":["1.10.0-preview.1"]}`, "", "no stable version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != "https://example.test/flat/microsoft.agents.ai/index.json" {
					t.Errorf("unexpected request: %s", request.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(test.versions)),
					ContentLength: int64(len(test.versions)), Header: make(http.Header),
				}, nil
			})}
			got, err := latestStableRelease(client, "https://example.test/flat/")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("latestStableRelease() = %q, %v; want %q", got, err, test.wantErr)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("latestStableRelease() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDownloadPackageDataLimit(t *testing.T) {
	for _, test := range []struct {
		name          string
		contentLength int64
		body          string
		wantErr       string
	}{
		{"at limit", 5, "abcde", ""},
		{"declared oversize", 6, "abcdef", "content exceeds"},
		{"streamed oversize", -1, "abcdef", "content exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(test.body)),
					ContentLength: test.contentLength, Header: make(http.Header),
				}, nil
			})}
			got, err := downloadPackageData(client, "https://example.test/package.nupkg", 5)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("download = %q, %v; want %q", got, err, test.wantErr)
				}
			} else if err != nil || string(got) != test.body {
				t.Fatalf("download = %q, %v; want %q", got, err, test.body)
			}
		})
	}
}

func TestDownloadCloseError(t *testing.T) {
	want := errors.New("response body close failed")
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Body: testBody{Reader: strings.NewReader("ok"), closeErr: want},
			ContentLength: 2, Header: make(http.Header),
		}, nil
	})}
	data, err := downloadPackageData(client, "https://example.test/package.nupkg", 5)
	if !errors.Is(err, want) || len(data) != 0 {
		t.Fatalf("close response = %q, %v; want close error and no data", data, err)
	}
}

func TestPackageArchive(t *testing.T) {
	manifest := []byte(`<package><metadata><id>Sample.Package</id><version>1.2.3</version></metadata></package>`)
	assembly := testAssembly(t)
	valid := []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net8.0/Sample.dll", assembly}, {"lib/net8.0/Bad.dll", []byte("bad")}}
	for _, test := range []struct {
		name    string
		entries []testEntry
		wantErr string
	}{
		{"missing manifest", nil, "no root package manifest"},
		{"duplicate manifests", []testEntry{{"first.nuspec", manifest}, {"second.nuspec", manifest}}, "multiple root package manifests"},
		{"wrong identity", []testEntry{{"Sample.Package.nuspec", []byte(`<package><metadata><id>Other</id><version>1.2.3</version></metadata></package>`)}}, "does not match requested"},
		{"no exact framework", []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net9.0/Sample.dll", assembly}}, "no exact assets"},
		{"no managed DLL", []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net8.0/readme.txt", []byte("text")}}, "contains no managed DLLs"},
		{"invalid assembly", []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net8.0/Bad.dll", []byte("bad")}}, "PE:"},
		{"reference asset preferred", valid, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := testArchive(t, test.entries...)
			result := inventory{
				Selection: selection{}, Packages: make(map[string]packageInfo),
				Assemblies: make(map[string]assemblyInfo), Types: make(map[string]typeInfo),
			}
			err := addPackage(&result, packageRequest{ID: "sample.package", Version: "1.2.3"}, "net8.0", "https://example.test/index.json", "https://example.test/Sample.Package.nupkg", archive)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("addPackage = %v; want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info, ok := result.Packages["Sample.Package"]
			if !ok || info.AssetGroup != "ref/net8.0" || !slices.Equal(info.Assemblies, []string{"Sample"}) || len(result.Types) != 2 {
				t.Fatalf("unexpected package inventory: %+v, types %+v", result.Packages, result.Types)
			}
		})
	}

	archive := testArchive(t, testEntry{"data.txt", []byte("abcdef")})
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readPackageEntry(zipReader.File[0], 5); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized ZIP entry: got %v, want limit error", err)
	}
	if data, err := readPackageEntry(zipReader.File[0], 6); err != nil || string(data) != "abcdef" {
		t.Fatalf("ZIP entry at limit: got %q, %v", data, err)
	}
}

type testWriter func([]byte) (int, error)

func (w testWriter) Write(p []byte) (int, error) { return w(p) }

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type testBody struct {
	io.Reader
	closeErr error
}

func (body testBody) Close() error { return body.closeErr }

type testEntry struct {
	name string
	data []byte
}

func testArchive(t *testing.T, entries ...testEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, item := range entries {
		entry, err := archive.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// testAssembly builds a small managed PE with two public types.
func testAssembly(t *testing.T) []byte {
	t.Helper()
	stringsHeap := []byte{0}
	addString := func(text string) uint16 {
		index := uint16(len(stringsHeap))
		stringsHeap = append(stringsHeap, text...)
		stringsHeap = append(stringsHeap, 0)
		return index
	}
	module := addString("Sample.dll")
	moduleType := addString("<Module>")
	widget := addString("Widget")
	gadget := addString("Gadget")
	hidden := addString("Hidden")
	parent := addString("Example")
	child := addString("Example.Child")
	assembly := addString("Sample")

	var rows bytes.Buffer
	writeRow := func(fields ...any) {
		for _, field := range fields {
			if err := binary.Write(&rows, binary.LittleEndian, field); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeRow(uint16(0), module, uint16(1), uint16(0), uint16(0)) // Module.
	writeType := func(flags uint32, name, namespace uint16) {
		writeRow(flags, name, namespace, uint16(0), uint16(1), uint16(1))
	}
	writeType(0, moduleType, 0)
	writeType(1, widget, parent)
	writeType(1, gadget, child)
	writeType(0, hidden, parent)
	writeRow(uint32(0), uint16(1), uint16(2), uint16(3), uint16(4), uint32(0), uint16(0), assembly, uint16(0)) // Assembly.

	tables := make([]byte, 24)
	tables[4], tables[7] = 2, 1
	binary.LittleEndian.PutUint64(tables[8:], 1<<0|1<<2|1<<32)
	for _, count := range []uint32{1, 4, 1} {
		tables = binary.LittleEndian.AppendUint32(tables, count)
	}
	tables = append(tables, rows.Bytes()...)
	streams := []testEntry{
		{"#~", tables}, {"#Strings", stringsHeap}, {"#GUID", make([]byte, 16)}, {"#Blob", []byte{0}},
	}
	root := make([]byte, 16)
	binary.LittleEndian.PutUint32(root, 0x424a5342) // BSJB metadata signature.
	binary.LittleEndian.PutUint16(root[4:], 1)
	binary.LittleEndian.PutUint16(root[6:], 1)
	const version = "v4.0.30319\x00\x00"
	binary.LittleEndian.PutUint32(root[12:], uint32(len(version)))
	root = append(root, version...)
	root = binary.LittleEndian.AppendUint16(root, 0)
	root = binary.LittleEndian.AppendUint16(root, uint16(len(streams)))
	offset := len(root)
	for _, stream := range streams {
		offset += 8 + (len(stream.name)+4)&^3
	}
	for _, stream := range streams {
		root = binary.LittleEndian.AppendUint32(root, uint32(offset))
		root = binary.LittleEndian.AppendUint32(root, uint32(len(stream.data)))
		root = append(root, stream.name...)
		root = append(root, make([]byte, 4-len(stream.name)%4)...)
		offset += (len(stream.data) + 3) &^ 3
	}
	for _, stream := range streams {
		root = append(root, stream.data...)
		root = append(root, make([]byte, (4-len(stream.data)%4)%4)...)
	}

	const sectionRVA, sectionOffset, cliSize = 0x2000, 0x200, 72
	section := make([]byte, cliSize)
	binary.LittleEndian.PutUint32(section, cliSize)
	binary.LittleEndian.PutUint16(section[4:], 2)
	binary.LittleEndian.PutUint16(section[6:], 5)
	binary.LittleEndian.PutUint32(section[8:], sectionRVA+cliSize)
	binary.LittleEndian.PutUint32(section[12:], uint32(len(root)))
	section = append(section, root...)
	optional := pe.OptionalHeader32{Magic: 0x10b, NumberOfRvaAndSizes: 16}
	optional.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_COM_DESCRIPTOR] = pe.DataDirectory{VirtualAddress: sectionRVA, Size: cliSize}
	var image bytes.Buffer
	for _, header := range []any{
		pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_I386, NumberOfSections: 1, SizeOfOptionalHeader: uint16(binary.Size(optional))},
		optional,
		pe.SectionHeader32{
			Name: [8]byte{'.', 't', 'e', 'x', 't'}, VirtualSize: uint32(len(section)), VirtualAddress: sectionRVA,
			SizeOfRawData: uint32(len(section)), PointerToRawData: sectionOffset,
		},
	} {
		if err := binary.Write(&image, binary.LittleEndian, header); err != nil {
			t.Fatal(err)
		}
	}
	if image.Len() > sectionOffset {
		t.Fatal("PE headers exceed section offset")
	}
	image.Write(make([]byte, sectionOffset-image.Len()))
	image.Write(section)
	return image.Bytes()
}
