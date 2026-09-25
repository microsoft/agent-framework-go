// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	defaultNuGetSource = "https://api.nuget.org/v3/index.json"
	maxPackageBytes    = 128 << 20
	maxManifestBytes   = 4 << 20
)

var (
	packageIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	packageVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)?(?:\+[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)?$`)
	frameworkPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
)

type releaseOptions struct {
	Version   string
	Framework string
	Source    string
	Packages  []string
}

type packageRequest struct {
	ID      string
	Version string
}

type packageManifest struct {
	XMLName  xml.Name `xml:"package"`
	Metadata struct {
		ID         string `xml:"id"`
		Version    string `xml:"version"`
		Repository struct {
			URL    string `xml:"url,attr"`
			Commit string `xml:"commit,attr"`
		} `xml:"repository"`
	} `xml:"metadata"`
}

func loadRelease(result *inventory, options releaseOptions, diagnostics io.Writer) error {
	version := options.Version
	if version != "latest" {
		var err error
		version, err = normalizePackageVersion(strings.TrimPrefix(version, "dotnet-"))
		if err != nil {
			return fmt.Errorf("-release: %w", err)
		}
	}
	if !frameworkPattern.MatchString(options.Framework) {
		return fmt.Errorf("invalid exact target framework %q", options.Framework)
	}
	framework := strings.ToLower(options.Framework)
	if err := publicHTTPSURL(options.Source); err != nil {
		return fmt.Errorf("-nuget-source: %w", err)
	}
	if len(options.Packages) == 0 {
		options.Packages = []string{
			"Microsoft.Agents.AI.Abstractions",
			"Microsoft.Agents.AI",
			"Microsoft.Agents.AI.Workflows",
		}
	}
	requests := make([]packageRequest, 0, len(options.Packages))
	needsLatest := false
	for _, value := range options.Packages {
		id, override, hasOverride := strings.Cut(value, "@")
		if !packageIDPattern.MatchString(id) {
			return fmt.Errorf("invalid NuGet package ID %q", id)
		}
		packageVersion := version
		if hasOverride {
			var err error
			packageVersion, err = normalizePackageVersion(override)
			if err != nil {
				return fmt.Errorf("package %q: %w", id, err)
			}
		}
		needsLatest = needsLatest || packageVersion == "latest"
		requests = append(requests, packageRequest{ID: strings.ToLower(id), Version: packageVersion})
	}
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many NuGet download redirects")
			}
			return publicHTTPSURL(request.URL.String())
		},
	}
	indexData, err := downloadPackageData(client, options.Source, maxManifestBytes)
	if err != nil {
		return fmt.Errorf("NuGet service index: %w", err)
	}
	var index struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("NuGet service index: %w", err)
	}
	var packageBase string
	for _, resource := range index.Resources {
		if resource.Type == "PackageBaseAddress/3.0.0" {
			packageBase = resource.ID
			break
		}
	}
	if err := publicHTTPSURL(packageBase); err != nil {
		return fmt.Errorf("NuGet service index has no usable HTTPS PackageBaseAddress/3.0.0: %w", err)
	}
	if needsLatest {
		version, err = latestStableRelease(client, packageBase)
		if err != nil {
			return fmt.Errorf("resolve latest stable MAF release: %w", err)
		}
		if _, err := fmt.Fprintf(diagnostics, "Resolved latest stable MAF release: %s\n", version); err != nil {
			return err
		}
	}
	byID := make(map[string]packageRequest)
	for _, request := range requests {
		if request.Version == "latest" {
			request.Version = version
		}
		if previous, exists := byID[request.ID]; exists && previous.Version != request.Version {
			return fmt.Errorf("package %q selected at multiple versions", request.ID)
		}
		byID[request.ID] = request
	}
	ordered := make([]string, 0, len(byID))
	for id := range byID {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	result.Packages = make(map[string]packageInfo)
	for _, id := range ordered {
		request := byID[id]
		address, err := url.JoinPath(packageBase, request.ID, request.Version, request.ID+"."+request.Version+".nupkg")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(diagnostics, "Reading NuGet package %s %s (%s)\n", request.ID, request.Version, framework); err != nil {
			return err
		}
		data, err := downloadPackageData(client, address, maxPackageBytes)
		if err != nil {
			return fmt.Errorf("package %s %s: %w", request.ID, request.Version, err)
		}
		if err := addPackage(result, request, framework, options.Source, address, data); err != nil {
			return fmt.Errorf("package %s %s: %w", request.ID, request.Version, err)
		}
	}
	return nil
}

// Resolve one framework release, rather than independently selecting a version
// for each package. Explicit package-version overrides remain pinned.
func latestStableRelease(client *http.Client, packageBase string) (string, error) {
	address, err := url.JoinPath(packageBase, "microsoft.agents.ai", "index.json")
	if err != nil {
		return "", err
	}
	data, err := downloadPackageData(client, address, maxManifestBytes)
	if err != nil {
		return "", err
	}
	var index struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return "", fmt.Errorf("Microsoft.Agents.AI version index: %w", err)
	}
	var latest string
	for _, candidate := range index.Versions {
		version, err := normalizePackageVersion(candidate)
		if err != nil {
			return "", fmt.Errorf("Microsoft.Agents.AI version index: %w", err)
		}
		if strings.Contains(version, "-") {
			continue
		}
		if latest == "" || compareStableVersions(version, latest) > 0 {
			latest = version
		}
	}
	if latest == "" {
		return "", fmt.Errorf("Microsoft.Agents.AI has no stable version in the selected feed")
	}
	return latest, nil
}

// Inputs are normalized stable versions. Compare numeric components, not the
// feed's ordering or lexicographic text (for example, 1.10 sorts above 1.9).
func compareStableVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 4 {
		x, y := "0", "0"
		if i < len(left) {
			x = left[i]
		}
		if i < len(right) {
			y = right[i]
		}
		if len(x) != len(y) {
			return len(x) - len(y)
		}
		if comparison := strings.Compare(x, y); comparison != 0 {
			return comparison
		}
	}
	return 0
}

// NuGet package addresses use normalized, lowercase exact versions. The
// release-level "latest" selector is resolved separately before downloading.
func normalizePackageVersion(value string) (string, error) {
	if !packageVersionPattern.MatchString(value) {
		return "", fmt.Errorf("expected an exact NuGet version such as 1.22.0, got %q", value)
	}
	// NuGet package identity and flat-container addresses omit build metadata.
	withoutMetadata, _, _ := strings.Cut(value, "+")
	core, prerelease, hasPrerelease := strings.Cut(withoutMetadata, "-")
	numbers := strings.Split(core, ".")
	for i, number := range numbers {
		numbers[i] = strings.TrimLeft(number, "0")
		if numbers[i] == "" {
			numbers[i] = "0"
		}
	}
	if len(numbers) == 4 && numbers[3] == "0" {
		numbers = numbers[:3]
	}
	normalized := strings.Join(numbers, ".")
	if hasPrerelease {
		normalized += "-" + strings.ToLower(prerelease)
	}
	return normalized, nil
}

func publicHTTPSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("expected an absolute HTTPS URL without credentials or a fragment")
	}
	return nil
}

func downloadPackageData(client *http.Client, address string, limit int64) (data []byte, err error) {
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "agent-framework-go/dotnetsymbols")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := response.Body.Close(); err == nil && closeErr != nil {
			data = nil
			err = fmt.Errorf("close GET %s: %w", address, closeErr)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", address, response.Status)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("GET %s: content exceeds %d-byte limit", address, limit)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: content exceeds %d-byte limit", address, limit)
	}
	return data, nil
}

func readPackageEntry(entry *zip.File, limit int64) (data []byte, err error) {
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("entry %q exceeds %d-byte limit", entry.Name, limit)
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := reader.Close(); err == nil && closeErr != nil {
			data = nil
			err = fmt.Errorf("close entry %q: %w", entry.Name, closeErr)
		}
	}()
	data, err = io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("entry %q exceeds %d-byte limit", entry.Name, limit)
	}
	return data, nil
}

func addPackage(result *inventory, request packageRequest, framework, source, address string, data []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("NuGet archive: %w", err)
	}
	var manifestFile *zip.File
	groups := make(map[string][]*zip.File)
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		parts := strings.Split(entry.Name, "/")
		if len(parts) == 1 && strings.HasSuffix(strings.ToLower(entry.Name), ".nuspec") {
			if manifestFile != nil {
				return fmt.Errorf("archive contains multiple root package manifests")
			}
			manifestFile = entry
		}
		if len(parts) == 3 && (parts[0] == "ref" || parts[0] == "lib") {
			group := parts[0] + "/" + strings.ToLower(parts[1])
			groups[group] = append(groups[group], entry)
		}
	}
	if manifestFile == nil {
		return fmt.Errorf("archive contains no root package manifest")
	}
	manifestData, err := readPackageEntry(manifestFile, maxManifestBytes)
	if err != nil {
		return err
	}
	var manifest packageManifest
	if err := xml.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("package manifest: %w", err)
	}
	metadata := manifest.Metadata
	version, err := normalizePackageVersion(metadata.Version)
	if err != nil || !strings.EqualFold(metadata.ID, request.ID) || version != request.Version {
		return fmt.Errorf("package manifest identity %q %q does not match requested %q %q", metadata.ID, metadata.Version, request.ID, request.Version)
	}
	group := "ref/" + framework
	if _, exists := groups[group]; !exists {
		group = "lib/" + framework
	}
	entries := groups[group]
	if len(entries) == 0 {
		available := make([]string, 0, len(groups))
		for name := range groups {
			available = append(available, name)
		}
		slices.Sort(available)
		return fmt.Errorf("no exact assets for %q; available groups: %s", framework, strings.Join(available, ", "))
	}
	slices.SortFunc(entries, func(a, b *zip.File) int { return strings.Compare(a.Name, b.Name) })
	info := packageInfo{
		Version: metadata.Version, Source: source, Download: address,
		SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Framework: framework, AssetGroup: group,
		Repository: metadata.Repository.URL, Commit: metadata.Repository.Commit,
		Assemblies: make([]string, 0),
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if !strings.HasSuffix(strings.ToLower(entry.Name), ".dll") {
			continue
		}
		key := strings.ToLower(entry.Name)
		if seen[key] {
			return fmt.Errorf("duplicate assembly asset %q", entry.Name)
		}
		seen[key] = true
		assemblyData, err := readPackageEntry(entry, maxPackageBytes)
		if err != nil {
			return err
		}
		name, assembly, types, err := extractAssemblyBytes(assemblyData, result.Selection)
		if err != nil {
			return fmt.Errorf("asset %q: %w", entry.Name, err)
		}
		if err := result.addAssembly(name, assembly, types); err != nil {
			return err
		}
		info.Assemblies = append(info.Assemblies, name)
	}
	if len(info.Assemblies) == 0 {
		return fmt.Errorf("asset group %q contains no managed DLLs; select an assembly-bearing package", group)
	}
	slices.Sort(info.Assemblies)
	info.Assemblies = slices.Compact(info.Assemblies)
	result.Packages[metadata.ID] = info
	return nil
}
