// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
	"github.com/microsoft/go-winmd/winmd"
)

func extractTestAssembly(file string) (string, testinventory.Assembly, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", testinventory.Assembly{}, err
	}
	return extractTestAssemblyBytes(data)
}

func extractTestAssemblyBytes(data []byte) (string, testinventory.Assembly, error) {
	name, info, extractor, err := readAssembly(data, symbolcatalog.Selection{})
	if err != nil {
		return "", testinventory.Assembly{}, err
	}
	if info.ReferenceAssembly {
		return "", testinventory.Assembly{}, fmt.Errorf("test assembly %q is a reference assembly; select its implementation assembly", name)
	}
	_, commit, _ := strings.Cut(info.InformationalVersion, "+")
	commit = strings.ToLower(commit)
	if len(commit) != 40 && len(commit) != 64 || strings.Trim(commit, "0123456789abcdef") != "" {
		commit = "" // Arbitrary build metadata is not a pinned source revision.
	}
	result := testinventory.Assembly{
		AssemblyMetadata: testinventory.AssemblyMetadata{
			Commit: commit, TargetFramework: info.TargetFramework, SHA256: info.SHA256,
		},
		Types: make(map[string][]string),
	}
	for index := range extractor.metadata.Tables.MethodDef.Indices() {
		attribute, err := extractor.testAttribute(index)
		if err != nil {
			return "", testinventory.Assembly{}, fmt.Errorf("MethodDef[%d]: %w", index, err)
		}
		if attribute == "" {
			continue
		}
		method, err := extractor.metadata.Tables.MethodDef.At(index)
		if err != nil {
			return "", testinventory.Assembly{}, err
		}
		owner, err := extractor.signatures.namedType(winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{
			Tag: winmd.TypeDefOrRefOrSpec_TypeDef, Index: extractor.methodOwners[index],
		}, 0)
		if err != nil {
			return "", testinventory.Assembly{}, err
		}
		if method.Name.String() == "" {
			return "", testinventory.Assembly{}, fmt.Errorf("%s MethodDef[%d]: empty test name", owner.name, index)
		}
		result.Types[owner.name] = append(result.Types[owner.name], method.Name.String())
	}
	if len(result.Types) == 0 {
		return "", testinventory.Assembly{}, fmt.Errorf("test assembly %q contains no supported test declarations", name)
	}
	for owner, methods := range result.Types {
		slices.Sort(methods)
		for i := 1; i < len(methods); i++ {
			if methods[i] == methods[i-1] {
				return "", testinventory.Assembly{}, fmt.Errorf("duplicate test name %s.%s; name-only identities cannot distinguish overloads", owner, methods[i])
			}
		}
	}
	return name, result, nil
}

func (e *assemblyExtractor) testAttribute(method winmd.Index) (string, error) {
	var marker string
	parent := winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_MethodDef, Index: method}
	for _, index := range e.attributesByParent[parent] {
		attribute, err := e.metadata.Tables.CustomAttribute.At(index)
		if err != nil {
			return "", err
		}
		name, err := e.attributeClassName(attribute.Type)
		if err != nil {
			return "", err
		}
		switch name {
		case "Xunit.FactAttribute", "Xunit.TheoryAttribute",
			"xRetry.v3.RetryFactAttribute", "xRetry.v3.RetryTheoryAttribute":
		default:
			short := strings.TrimSuffix(name, "Attribute")
			if strings.HasSuffix(short, "Fact") || strings.HasSuffix(short, "Theory") {
				return "", fmt.Errorf("unsupported test attribute %q", name)
			}
			continue
		}
		if marker != "" {
			return "", fmt.Errorf("multiple test attributes: %s and %s", marker, name)
		}
		// Only the constructor's declaring type is needed. Do not decode or
		// execute arbitrary arguments, data providers, or custom discoverers.
		marker = name
	}
	return marker, nil
}
