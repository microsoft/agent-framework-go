// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"fmt"

	"github.com/microsoft/go-winmd/winmd"
)

const (
	experimentalAttributeName         = "System.Diagnostics.CodeAnalysis.ExperimentalAttribute"
	compilerGeneratedAttributeName    = "System.Runtime.CompilerServices.CompilerGeneratedAttribute"
	nullableAttributeName             = "System.Runtime.CompilerServices.NullableAttribute"
	informationalVersionAttributeName = "System.Reflection.AssemblyInformationalVersionAttribute"
	targetFrameworkAttributeName      = "System.Runtime.Versioning.TargetFrameworkAttribute"
	referenceAssemblyAttributeName    = "System.Runtime.CompilerServices.ReferenceAssemblyAttribute"
)

type attributeSummary struct {
	attributes
	informationalVersion string
	targetFramework      string
	referenceAssembly    bool
}

func (e *assemblyExtractor) readAttributes(parent winmd.CodedIndex[winmd.HasCustomAttribute]) (attributeSummary, error) {
	var result attributeSummary
	decodedNames := make(map[string]bool)
	for _, index := range e.attributesByParent[parent] {
		attribute, err := e.metadata.Tables.CustomAttribute.At(index)
		if err != nil {
			return attributeSummary{}, err
		}
		name, err := e.attributeClassName(attribute.Type)
		if err != nil {
			return attributeSummary{}, fmt.Errorf("CustomAttribute[%d] constructor: %w", index, err)
		}
		switch name {
		case compilerGeneratedAttributeName:
			result.CompilerGenerated = true
			continue
		case referenceAssemblyAttributeName:
			result.referenceAssembly = true
			continue
		case nullableAttributeName:
			switch parent.Tag {
			case winmd.HasCustomAttribute_Param, winmd.HasCustomAttribute_Property, winmd.HasCustomAttribute_Event, winmd.HasCustomAttribute_Field:
			default:
				continue
			}
		case experimentalAttributeName, informationalVersionAttributeName, targetFrameworkAttributeName:
		default:
			// Decode only values used by reconciliation or assembly provenance.
			// Arbitrary values can require external enum resolution.
			continue
		}
		if decodedNames[name] {
			return attributeSummary{}, fmt.Errorf("CustomAttribute[%d]: duplicate %s on %s[%d]", index, name, parent.Tag, parent.Index)
		}
		decodedNames[name] = true
		value, err := e.attributeDecoder.Decode(attribute)
		if err != nil {
			return attributeSummary{}, fmt.Errorf("CustomAttribute[%d] %s: %w", index, name, err)
		}
		if err := result.addKnownAttribute(name, value); err != nil {
			return attributeSummary{}, fmt.Errorf("CustomAttribute[%d] %s: %w", index, name, err)
		}
	}
	return result, nil
}

func (e *assemblyExtractor) attributeClassName(index winmd.CodedIndex[winmd.CustomAttributeType]) (string, error) {
	if name, ok := e.attributeTypes[index]; ok {
		return name, nil
	}
	var typ winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]
	var signature winmd.SigMethodDefBlob
	switch index.Tag {
	case winmd.CustomAttributeType_MethodDef:
		method, err := e.metadata.Tables.MethodDef.At(index.Index)
		if err != nil {
			return "", err
		}
		if method.Name.String() != ".ctor" {
			return "", fmt.Errorf("MethodDef[%d] %q is not an attribute constructor", index.Index, method.Name)
		}
		if method.Flags.HasAll(winmd.MethodFlags_Static) {
			return "", fmt.Errorf("MethodDef[%d]: attribute constructor must be an instance method", index.Index)
		}
		signature = method.Signature
		owner, ok := e.methodOwners[index.Index]
		if !ok {
			return "", fmt.Errorf("MethodDef[%d]: attribute constructor has no declaring TypeDef", index.Index)
		}
		typ = winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{Tag: winmd.TypeDefOrRefOrSpec_TypeDef, Index: owner}
	case winmd.CustomAttributeType_MemberRef:
		member, err := e.metadata.Tables.MemberRef.At(index.Index)
		if err != nil {
			return "", err
		}
		if member.Name.String() != ".ctor" {
			return "", fmt.Errorf("MemberRef[%d] %q is not an attribute constructor", index.Index, member.Name)
		}
		signature = winmd.SigMethodDefBlob(member.Signature)
		typ.Index = member.Class.Index
		switch member.Class.Tag {
		case winmd.MemberRefParent_TypeDef:
			typ.Tag = winmd.TypeDefOrRefOrSpec_TypeDef
		case winmd.MemberRefParent_TypeRef:
			typ.Tag = winmd.TypeDefOrRefOrSpec_TypeRef
		case winmd.MemberRefParent_TypeSpec:
			typ.Tag = winmd.TypeDefOrRefOrSpec_TypeSpec
		default:
			return "", fmt.Errorf("MemberRef[%d]: unsupported attribute constructor parent %v", index.Index, member.Class.Tag)
		}
	default:
		return "", fmt.Errorf("unsupported custom attribute constructor tag %v", index.Tag)
	}
	// Constructor shape can be checked without decoding arbitrary attribute
	// arguments or resolving external enum types. Constructors use the same
	// non-vararg signature grammar for MethodDef and MemberRef.
	sig, err := e.metadata.MethodDefSignature(signature)
	if err != nil {
		return "", fmt.Errorf("%s[%d] signature: %w", index.Tag, index.Index, err)
	}
	if !sig.HasThis || sig.ExplicitThis || sig.VarArgs || sig.Generic != 0 || sig.RetType.Type.Kind != winmd.ElementType_VOID {
		return "", fmt.Errorf("%s[%d]: invalid attribute constructor signature", index.Tag, index.Index)
	}
	name, err := e.signatures.typeHandle(typ, 0)
	if err != nil {
		return "", err
	}
	e.attributeTypes[index] = name
	return name, nil
}

func (summary *attributeSummary) addKnownAttribute(name string, value winmd.CustomAttributeValue) error {
	arguments := value.FixedArguments
	if len(arguments) != 1 {
		return fmt.Errorf("expected one constructor argument, got %d", len(arguments))
	}
	switch name {
	case experimentalAttributeName, informationalVersionAttributeName, targetFrameworkAttributeName:
		text, err := attributeString(arguments[0])
		if err != nil {
			return err
		}
		switch name {
		case experimentalAttributeName:
			summary.Experimental = text
		case informationalVersionAttributeName:
			summary.informationalVersion = text
		case targetFrameworkAttributeName:
			summary.targetFramework = text
		}
	case nullableAttributeName:
		flags, err := attributeNullableFlags(arguments[0])
		if err != nil {
			return err
		}
		summary.NullableFlags = flags
	default:
		return fmt.Errorf("unsupported attribute value %s", name)
	}
	return nil
}

func attributeString(argument winmd.CustomAttributeArgument) (string, error) {
	if argument.Type.Kind == winmd.ElementType_STRING {
		if argument.Value == nil {
			return "", nil
		}
		if text, ok := argument.Value.(string); ok {
			return text, nil
		}
	}
	return "", fmt.Errorf("expected String argument, got %v (%T)", argument.Type.Kind, argument.Value)
}

func attributeByte(argument winmd.CustomAttributeArgument) (byte, error) {
	if value, ok := argument.Value.(byte); argument.Type.Kind == winmd.ElementType_U1 && ok {
		return value, nil
	}
	return 0, fmt.Errorf("expected Byte argument, got %v (%T)", argument.Type.Kind, argument.Value)
}

func attributeNullableFlags(argument winmd.CustomAttributeArgument) ([]int, error) {
	if argument.Type.Kind == winmd.ElementType_U1 {
		flag, err := attributeByte(argument)
		if err != nil {
			return nil, err
		}
		return []int{int(flag)}, nil
	}
	if argument.Type.Kind != winmd.ElementType_SZARRAY || argument.Type.Element == nil || argument.Type.Element.Kind != winmd.ElementType_U1 {
		return nil, fmt.Errorf("expected Byte or Byte[] nullable flags, got %v", argument.Type.Kind)
	}
	if argument.Value == nil {
		return nil, nil
	}
	values, ok := argument.Value.([]winmd.CustomAttributeArgument)
	if !ok {
		return nil, fmt.Errorf("expected custom attribute argument array, got %T", argument.Value)
	}
	flags := make([]int, len(values))
	for i, value := range values {
		flag, err := attributeByte(value)
		if err != nil {
			return nil, fmt.Errorf("nullable flag %d: %w", i, err)
		}
		flags[i] = int(flag)
	}
	return flags, nil
}
