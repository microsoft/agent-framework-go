// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"crypto/sha1"
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/go-winmd/winmd"
)

const maxTypeDepth = 64

type metadataTypeName struct {
	name      string
	namespace string
	origin    string
	depth     int
}

// signatureRenderer never resolves a reference outside the input metadata.
// Names retain CLR arity suffixes and use '+' for nesting, not C# spelling.
type signatureRenderer struct {
	metadata *winmd.Metadata
	parents  map[winmd.Index]winmd.Index
	names    map[winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]]metadataTypeName
	active   map[winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]]bool
	origins  map[string]string
	assembly string
	module   string
}

func newSignatureRenderer(metadata *winmd.Metadata) (*signatureRenderer, error) {
	assembly, err := metadata.Tables.Assembly.At(0)
	if err != nil {
		return nil, err
	}
	module, err := metadata.Tables.Module.At(0)
	if err != nil {
		return nil, err
	}
	r := &signatureRenderer{
		metadata: metadata,
		parents:  make(map[winmd.Index]winmd.Index),
		names:    make(map[winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]]metadataTypeName),
		active:   make(map[winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]]bool),
		origins:  make(map[string]string),
		assembly: assemblyOrigin(assembly.Name.String(), assembly.MajorVersion, assembly.MinorVersion, assembly.BuildNumber, assembly.RevisionNumber, assembly.Culture.String(), assembly.PublicKey, assembly.Flags),
		module:   module.Name.String(),
	}
	for nested, err := range metadata.Tables.NestedClass.All() {
		if err != nil {
			return nil, err
		}
		if _, exists := r.parents[nested.NestedClass]; exists {
			return nil, fmt.Errorf("NestedClass: duplicate enclosing type for TypeDef[%d]", nested.NestedClass)
		}
		r.parents[nested.NestedClass] = nested.EnclosingClass
	}
	return r, nil
}

func (r *signatureRenderer) namedType(index winmd.CodedIndex[winmd.TypeDefOrRefOrSpec], depth int) (metadataTypeName, error) {
	if depth >= maxTypeDepth || r.active[index] {
		return metadataTypeName{}, fmt.Errorf("%s[%d]: cyclic or excessively nested type name", index.Tag, index.Index)
	}
	if name, ok := r.names[index]; ok {
		if depth+name.depth > maxTypeDepth {
			return metadataTypeName{}, fmt.Errorf("%s[%d]: type name nesting limit exceeded", index.Tag, index.Index)
		}
		return name, nil
	}
	r.active[index] = true
	defer delete(r.active, index)
	var name, namespace, origin string
	var parent winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]
	var nested bool
	switch index.Tag {
	case winmd.TypeDefOrRefOrSpec_TypeDef:
		typ, err := r.metadata.Tables.TypeDef.At(index.Index)
		if err != nil {
			return metadataTypeName{}, err
		}
		name, namespace = typ.Name.String(), typ.Namespace.String()
		origin = r.assembly
		var enclosing winmd.Index
		enclosing, nested = r.parents[index.Index]
		if nested != typ.Flags.Visibility().IsNested() {
			return metadataTypeName{}, fmt.Errorf("TypeDef[%d] %q: visibility and NestedClass ownership disagree", index.Index, name)
		}
		parent = winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{Tag: winmd.TypeDefOrRefOrSpec_TypeDef, Index: enclosing}
	case winmd.TypeDefOrRefOrSpec_TypeRef:
		typ, err := r.metadata.Tables.TypeRef.At(index.Index)
		if err != nil {
			return metadataTypeName{}, err
		}
		name, namespace = typ.Name.String(), typ.Namespace.String()
		switch typ.ResolutionScope.Tag {
		case winmd.ResolutionScope_TypeRef:
			nested = true
			parent = winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{Tag: winmd.TypeDefOrRefOrSpec_TypeRef, Index: typ.ResolutionScope.Index}
		case winmd.ResolutionScope_Module:
			origin = r.assembly
		case winmd.ResolutionScope_ModuleRef:
			module, err := r.metadata.Tables.ModuleRef.At(typ.ResolutionScope.Index)
			if err != nil {
				return metadataTypeName{}, err
			}
			origin = r.assembly
			if module.Name.String() != r.module {
				origin += ", Module=" + module.Name.String()
			}
		case winmd.ResolutionScope_AssemblyRef:
			assembly, err := r.metadata.Tables.AssemblyRef.At(typ.ResolutionScope.Index)
			if err != nil {
				return metadataTypeName{}, err
			}
			origin = assemblyOrigin(assembly.Name.String(), assembly.MajorVersion, assembly.MinorVersion, assembly.BuildNumber, assembly.RevisionNumber, assembly.Culture.String(), assembly.PublicKeyOrToken, assembly.Flags)
		case winmd.ResolutionScope_Null:
			origin = "unresolved scope in " + r.assembly
		default:
			return metadataTypeName{}, fmt.Errorf("TypeRef[%d]: unsupported resolution scope %v", index.Index, typ.ResolutionScope.Tag)
		}
	default:
		return metadataTypeName{}, fmt.Errorf("%s[%d]: expected TypeDef or TypeRef", index.Tag, index.Index)
	}
	if name == "" {
		return metadataTypeName{}, fmt.Errorf("%s[%d]: empty type name", index.Tag, index.Index)
	}
	result := metadataTypeName{name: name, namespace: namespace, origin: origin, depth: 1}
	if nested {
		enclosing, err := r.namedType(parent, depth+1)
		if err != nil {
			return metadataTypeName{}, err
		}
		result.name = enclosing.name + "+" + name
		result.namespace = enclosing.namespace
		result.origin = enclosing.origin
		result.depth = enclosing.depth + 1
	} else if namespace != "" {
		result.name = namespace + "." + name
	}
	if previous, exists := r.origins[result.name]; exists && previous != result.origin {
		return metadataTypeName{}, fmt.Errorf("ambiguous type name %q from %q and %q; assembly-qualified signatures are not supported", result.name, previous, result.origin)
	}
	r.origins[result.name] = result.origin
	r.names[index] = result
	return result, nil
}

// Scope is used only to reject ambiguous unqualified names, not to load or
// resolve dependencies. SHA-1 is the CLR public-key-token algorithm, not a
// security decision made by the extractor.
func assemblyOrigin(name string, major, minor, build, revision uint16, culture string, key []byte, flags winmd.AssemblyFlags) string {
	if len(key) != 0 && flags&winmd.AssemblyFlags_PublicKey != 0 {
		hash := sha1.Sum(key)
		key = make([]byte, 8)
		for i := range key {
			key[i] = hash[len(hash)-1-i]
		}
	}
	return fmt.Sprintf("%s, Version=%d.%d.%d.%d, Culture=%s, PublicKeyToken=%x", strings.ToLower(name), major, minor, build, revision, strings.ToLower(culture), key)
}

func (r *signatureRenderer) typeDefOrRef(index winmd.CodedIndex[winmd.TypeDefOrRef]) (string, error) {
	switch index.Tag {
	case winmd.TypeDefOrRef_TypeDef, winmd.TypeDefOrRef_TypeRef, winmd.TypeDefOrRef_TypeSpec:
		return r.typeHandle(winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{Tag: winmd.TypeDefOrRefOrSpec(index.Tag), Index: index.Index}, 0)
	default:
		return "", fmt.Errorf("expected TypeDef, TypeRef, or TypeSpec, got %v", index.Tag)
	}
}

func (r *signatureRenderer) typeHandle(index winmd.CodedIndex[winmd.TypeDefOrRefOrSpec], depth int) (string, error) {
	if index.Tag != winmd.TypeDefOrRefOrSpec_TypeSpec {
		name, err := r.namedType(index, depth)
		return name.name, err
	}
	if depth >= maxTypeDepth || r.active[index] {
		return "", fmt.Errorf("TypeSpec[%d]: cyclic or excessively nested type specification", index.Index)
	}
	r.active[index] = true
	defer delete(r.active, index)
	typ, err := r.metadata.Tables.TypeSpec.At(index.Index)
	if err != nil {
		return "", err
	}
	sig, err := r.metadata.TypeSpecSignature(typ.Signature)
	if err != nil {
		return "", fmt.Errorf("TypeSpec[%d] signature: %w", index.Index, err)
	}
	text, err := r.renderType(winmd.SigType{Kind: sig.Kind, Value: sig.Value}, depth+1)
	if err != nil {
		return "", fmt.Errorf("TypeSpec[%d]: %w", index.Index, err)
	}
	return text, nil
}

func primitiveTypeName(kind winmd.ElementType) string {
	switch kind {
	case winmd.ElementType_VOID:
		return "System.Void"
	case winmd.ElementType_BOOLEAN:
		return "System.Boolean"
	case winmd.ElementType_CHAR:
		return "System.Char"
	case winmd.ElementType_I1:
		return "System.SByte"
	case winmd.ElementType_U1:
		return "System.Byte"
	case winmd.ElementType_I2:
		return "System.Int16"
	case winmd.ElementType_U2:
		return "System.UInt16"
	case winmd.ElementType_I4:
		return "System.Int32"
	case winmd.ElementType_U4:
		return "System.UInt32"
	case winmd.ElementType_I8:
		return "System.Int64"
	case winmd.ElementType_U8:
		return "System.UInt64"
	case winmd.ElementType_R4:
		return "System.Single"
	case winmd.ElementType_R8:
		return "System.Double"
	case winmd.ElementType_STRING:
		return "System.String"
	case winmd.ElementType_OBJECT:
		return "System.Object"
	case winmd.ElementType_I:
		return "System.IntPtr"
	case winmd.ElementType_U:
		return "System.UIntPtr"
	case winmd.ElementType_TYPEDBYREF:
		return "System.TypedReference"
	default:
		return ""
	}
}

func (r *signatureRenderer) renderType(typ winmd.SigType, depth int) (string, error) {
	if depth >= maxTypeDepth {
		return "", fmt.Errorf("signature type nesting limit exceeded")
	}
	text := primitiveTypeName(typ.Kind)
	if text != "" {
		if typ.Value != nil {
			return "", fmt.Errorf("%v: unexpected primitive value %T", typ.Kind, typ.Value)
		}
	} else {
		switch typ.Kind {
		case winmd.ElementType_CLASS, winmd.ElementType_VALUETYPE:
			index, ok := typ.Value.(winmd.CodedIndex[winmd.TypeDefOrRefOrSpec])
			if !ok {
				return "", fmt.Errorf("%v: expected type handle, got %T", typ.Kind, typ.Value)
			}
			var err error
			text, err = r.typeHandle(index, depth+1)
			if err != nil {
				return "", err
			}
		case winmd.ElementType_VAR, winmd.ElementType_MVAR:
			number, ok := typ.Value.(uint32)
			if !ok {
				return "", fmt.Errorf("%v: expected uint32 generic parameter number, got %T", typ.Kind, typ.Value)
			}
			text = "!"
			if typ.Kind == winmd.ElementType_MVAR {
				text += "!"
			}
			text += strconv.FormatUint(uint64(number), 10)
		case winmd.ElementType_PTR, winmd.ElementType_BYREF, winmd.ElementType_SZARRAY:
			element, ok := typ.Value.(winmd.SigType)
			if !ok {
				return "", fmt.Errorf("%v: expected SigType, got %T", typ.Kind, typ.Value)
			}
			var err error
			text, err = r.renderType(element, depth+1)
			if err != nil {
				return "", err
			}
			switch typ.Kind {
			case winmd.ElementType_PTR:
				text += "*"
			case winmd.ElementType_BYREF:
				text += "&"
			case winmd.ElementType_SZARRAY:
				text += "[]"
			}
		case winmd.ElementType_ARRAY:
			array, ok := typ.Value.(winmd.SigArray)
			if !ok {
				return "", fmt.Errorf("ARRAY: expected SigArray, got %T", typ.Value)
			}
			shape, err := arrayShape(array)
			if err != nil {
				return "", err
			}
			text, err = r.renderType(array.Type, depth+1)
			if err != nil {
				return "", err
			}
			text += shape
		case winmd.ElementType_GENERICINST:
			inst, ok := typ.Value.(winmd.SigGenericInst)
			if !ok || len(inst.Type) == 0 {
				return "", fmt.Errorf("GENERICINST: expected nonempty SigGenericInst, got %T", typ.Value)
			}
			var err error
			text, err = r.typeHandle(inst.Index, depth+1)
			if err != nil {
				return "", err
			}
			arguments := make([]string, len(inst.Type))
			for i, argument := range inst.Type {
				arguments[i], err = r.renderType(argument, depth+1)
				if err != nil {
					return "", fmt.Errorf("generic argument %d: %w", i, err)
				}
			}
			text += "<" + strings.Join(arguments, ",") + ">"
		case winmd.ElementType_FNPTR:
			sig, ok := typ.Value.(winmd.SigStandAloneMethod)
			if !ok {
				return "", fmt.Errorf("FNPTR: expected SigStandAloneMethod, got %T", typ.Value)
			}
			var err error
			text, err = r.functionPointer(sig, depth+1)
			if err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported signature element type %v", typ.Kind)
		}
	}
	for _, mod := range typ.Mod {
		var kind string
		switch mod.Kind {
		case winmd.SigCustomModKind_Reqd:
			kind = "modreq"
		case winmd.SigCustomModKind_Opt:
			kind = "modopt"
		default:
			return "", fmt.Errorf("unsupported custom modifier kind %v", mod.Kind)
		}
		name, err := r.typeHandle(mod.Index, depth+1)
		if err != nil {
			return "", err
		}
		text += " " + kind + "(" + name + ")"
	}
	return text, nil
}

// Each rectangular dimension is lower-bound:size. An omitted bound or size
// stays empty, so [:], [0:], and [0:5] remain distinct from each other and [].
func arrayShape(array winmd.SigArray) (string, error) {
	// The CLR supports at most 32 dimensions. Do not allocate from a huge rank
	// encoded in an otherwise tiny malformed signature.
	if array.Rank == 0 || array.Rank > 32 || uint64(len(array.Sizes)) > uint64(array.Rank) || uint64(len(array.LowerBounds)) > uint64(array.Rank) {
		return "", fmt.Errorf("unsupported or invalid ARRAY shape: rank %d, %d sizes, %d lower bounds", array.Rank, len(array.Sizes), len(array.LowerBounds))
	}
	dimensions := make([]string, int(array.Rank))
	for i := range dimensions {
		if i < len(array.LowerBounds) {
			dimensions[i] = strconv.FormatInt(int64(array.LowerBounds[i]), 10)
		}
		dimensions[i] += ":"
		if i < len(array.Sizes) {
			dimensions[i] += strconv.FormatUint(uint64(array.Sizes[i]), 10)
		}
	}
	return "[" + strings.Join(dimensions, ",") + "]", nil
}

// Function pointers retain their calling convention, instance bits, generic
// arity, and SENTINEL. The outer parentheses disambiguate return modifiers
// from modifiers or pointer/array suffixes on the function-pointer type.
func (r *signatureRenderer) functionPointer(sig winmd.SigStandAloneMethod, depth int) (string, error) {
	var convention string
	switch sig.CallingConvention {
	case winmd.SigCallingConvention_Default:
		convention = "default"
	case winmd.SigCallingConvention_Cdecl:
		convention = "cdecl"
	case winmd.SigCallingConvention_Stdcall:
		convention = "stdcall"
	case winmd.SigCallingConvention_Thiscall:
		convention = "thiscall"
	case winmd.SigCallingConvention_Fastcall:
		convention = "fastcall"
	case winmd.SigCallingConvention_Vararg:
		convention = "vararg"
	default:
		return "", fmt.Errorf("FNPTR: unsupported calling convention %#x", sig.CallingConvention)
	}
	if sig.HasThis {
		convention += ",hasthis"
	}
	if sig.ExplicitThis {
		convention += ",explicitthis"
	}
	var parameters []string
	for i, param := range sig.Param {
		text, err := r.renderType(param.Type, depth)
		if err != nil {
			return "", fmt.Errorf("FNPTR parameter %d: %w", i, err)
		}
		parameters = append(parameters, text)
	}
	if len(sig.VariableParam) != 0 {
		parameters = append(parameters, "...")
		for i, param := range sig.VariableParam {
			text, err := r.renderType(param.Type, depth)
			if err != nil {
				return "", fmt.Errorf("FNPTR optional parameter %d: %w", i, err)
			}
			parameters = append(parameters, text)
		}
	}
	result, err := r.renderType(sig.RetType.Type, depth)
	if err != nil {
		return "", fmt.Errorf("FNPTR return type: %w", err)
	}
	return "fnptr[" + convention + "]" + genericArity(sig.Generic) + "((" + strings.Join(parameters, ",") + ") -> " + result + ")", nil
}

func genericArity(number uint32) string {
	if number == 0 {
		return ""
	}
	return "``" + strconv.FormatUint(uint64(number), 10)
}

func parameterTypes(parameters []parameterInfo) string {
	types := make([]string, len(parameters))
	for i, parameter := range parameters {
		types[i] = parameter.Type
	}
	return strings.Join(types, ",")
}

func methodIdentity(name string, arity uint32, parameters []parameterInfo, result string, varArgs bool) string {
	args := parameterTypes(parameters)
	if varArgs {
		if args != "" {
			args += ","
		}
		args += "..."
	}
	if name == ".ctor" {
		return name + "(" + args + ")"
	}
	return name + genericArity(arity) + "(" + args + ") -> " + result
}

func propertyIdentity(name string, parameters []parameterInfo, result string) string {
	if len(parameters) != 0 {
		name += "(" + parameterTypes(parameters) + ")"
	}
	return name + " -> " + result
}
