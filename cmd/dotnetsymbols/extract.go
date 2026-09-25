// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"debug/pe"
	"fmt"
	"os"
	"slices"

	"github.com/microsoft/go-winmd/winmd"
)

type assemblyExtractor struct {
	metadata           *winmd.Metadata
	selected           selection
	signatures         *signatureRenderer
	attributeDecoder   *winmd.CustomAttributeDecoder
	attributesByParent map[winmd.CodedIndex[winmd.HasCustomAttribute]][]winmd.Index
	attributeTypes     map[winmd.CodedIndex[winmd.CustomAttributeType]]string
	constantsByParent  map[winmd.CodedIndex[winmd.HasConstant]]winmd.Index
	methodOwners       map[winmd.Index]winmd.Index
	propertiesByType   map[winmd.Index]winmd.Slice
	eventsByType       map[winmd.Index]winmd.Slice
	semantics          map[winmd.CodedIndex[winmd.HasSemantics]][]winmd.Index
	accessorMethods    map[winmd.Index]bool
	genericsByOwner    map[winmd.CodedIndex[winmd.TypeOrMethodDef]][]winmd.Index
	constraintsByParam map[winmd.Index][]winmd.Index
}

// extractAssembly reads and hashes the same bytes. No CLR code is loaded or
// executed, and no referenced assembly or module is opened. Errors discard the
// entire result rather than returning an apparently complete partial inventory.
func extractAssembly(file string, selected selection) (string, assemblyInfo, map[string]typeInfo, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", assemblyInfo{}, nil, err
	}
	return extractAssemblyBytes(data, selected)
}

func extractAssemblyBytes(data []byte, selected selection) (assemblyName string, info assemblyInfo, types map[string]typeInfo, err error) {
	image, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return "", assemblyInfo{}, nil, fmt.Errorf("PE: %w", err)
	}
	metadata, err := winmd.New(image)
	if err != nil {
		return "", assemblyInfo{}, nil, fmt.Errorf("CLI metadata: %w", err)
	}
	if count := metadata.Tables.Assembly.Len(); count != 1 {
		return "", assemblyInfo{}, nil, fmt.Errorf("assembly: expected exactly one manifest row, got %d; netmodules are not supported", count)
	}
	assembly, err := metadata.Tables.Assembly.At(0)
	if err != nil {
		return "", assemblyInfo{}, nil, err
	}
	assemblyName = assembly.Name.String()
	if assemblyName == "" {
		return "", assemblyInfo{}, nil, fmt.Errorf("Assembly[0]: empty assembly name")
	}
	if count := metadata.Tables.Module.Len(); count != 1 {
		return "", assemblyInfo{}, nil, fmt.Errorf("module: expected exactly one manifest module, got %d", count)
	}
	if _, err := metadata.Tables.Module.At(0); err != nil {
		return "", assemblyInfo{}, nil, err
	}
	for index := range metadata.Tables.File.Indices() {
		file, err := metadata.Tables.File.At(index)
		if err != nil {
			return "", assemblyInfo{}, nil, err
		}
		if file.Flags.Content() == winmd.FileContent_ContainsMetaData {
			return "", assemblyInfo{}, nil, fmt.Errorf("file[%d] %q: multi-module assemblies are not supported", index, file.Name)
		}
	}
	extractor, err := newAssemblyExtractor(metadata, selected)
	if err != nil {
		return "", assemblyInfo{}, nil, err
	}
	assemblyAttributes, err := extractor.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_Assembly, Index: 0})
	if err != nil {
		return "", assemblyInfo{}, nil, fmt.Errorf("Assembly[0] %q: %w", assemblyName, err)
	}
	forwarded, err := extractor.forwardedTypes()
	if err != nil {
		return "", assemblyInfo{}, nil, err
	}
	types = make(map[string]typeInfo)
	for index := range metadata.Tables.TypeDef.Indices() {
		name, err := extractor.signatures.namedType(winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{Tag: winmd.TypeDefOrRefOrSpec_TypeDef, Index: index}, 0)
		if err != nil {
			return "", assemblyInfo{}, nil, err
		}
		if !includesNamespace(name.namespace, selected) {
			continue
		}
		visible, err := extractor.externallyVisible(index)
		if err != nil {
			return "", assemblyInfo{}, nil, err
		}
		if !visible {
			continue
		}
		typ, err := extractor.readType(index, assemblyName, name.name)
		if err != nil {
			return "", assemblyInfo{}, nil, fmt.Errorf("TypeDef[%d] %s: %w", index, name.name, err)
		}
		if _, exists := types[name.name]; exists {
			return "", assemblyInfo{}, nil, fmt.Errorf("TypeDef[%d]: duplicate canonical type %q", index, name.name)
		}
		if _, exists := forwarded[name.name]; exists {
			return "", assemblyInfo{}, nil, fmt.Errorf("TypeDef[%d]: canonical type %q is both defined and forwarded", index, name.name)
		}
		types[name.name] = typ
	}
	info = assemblyInfo{
		Version:              fmt.Sprintf("%d.%d.%d.%d", assembly.MajorVersion, assembly.MinorVersion, assembly.BuildNumber, assembly.RevisionNumber),
		InformationalVersion: assemblyAttributes.informationalVersion,
		TargetFramework:      assemblyAttributes.targetFramework,
		SHA256:               fmt.Sprintf("%x", sha256.Sum256(data)),
		ReferenceAssembly:    assemblyAttributes.referenceAssembly,
		ForwardedTypes:       forwarded,
	}
	return assemblyName, info, types, nil
}

func newAssemblyExtractor(metadata *winmd.Metadata, selected selection) (*assemblyExtractor, error) {
	signatures, err := newSignatureRenderer(metadata)
	if err != nil {
		return nil, err
	}
	e := &assemblyExtractor{
		metadata:           metadata,
		selected:           selected,
		signatures:         signatures,
		attributeDecoder:   winmd.NewCustomAttributeDecoder(metadata),
		attributesByParent: make(map[winmd.CodedIndex[winmd.HasCustomAttribute]][]winmd.Index),
		attributeTypes:     make(map[winmd.CodedIndex[winmd.CustomAttributeType]]string),
		constantsByParent:  make(map[winmd.CodedIndex[winmd.HasConstant]]winmd.Index),
		methodOwners:       make(map[winmd.Index]winmd.Index),
		propertiesByType:   make(map[winmd.Index]winmd.Slice),
		eventsByType:       make(map[winmd.Index]winmd.Slice),
		semantics:          make(map[winmd.CodedIndex[winmd.HasSemantics]][]winmd.Index),
		accessorMethods:    make(map[winmd.Index]bool),
		genericsByOwner:    make(map[winmd.CodedIndex[winmd.TypeOrMethodDef]][]winmd.Index),
		constraintsByParam: make(map[winmd.Index][]winmd.Index),
	}
	if err := e.indexMetadata(); err != nil {
		return nil, err
	}
	return e, nil
}

// Index table ownership without parsing private or filtered-out signatures.
func (e *assemblyExtractor) indexMetadata() error {
	tables := e.metadata.Tables
	typeNames := make(map[string]winmd.Index)
	for index := range tables.TypeDef.Indices() {
		typ, err := tables.TypeDef.At(index)
		if err != nil {
			return err
		}
		name, err := e.signatures.namedType(winmd.CodedIndex[winmd.TypeDefOrRefOrSpec]{Tag: winmd.TypeDefOrRefOrSpec_TypeDef, Index: index}, 0)
		if err != nil {
			return err
		}
		if previous, exists := typeNames[name.name]; exists {
			return fmt.Errorf("TypeDef[%d] and TypeDef[%d]: duplicate canonical type %q", previous, index, name.name)
		}
		typeNames[name.name] = index
		for method := range typ.MethodList.All() {
			if _, exists := e.methodOwners[method]; exists {
				return fmt.Errorf("MethodDef[%d]: multiple declaring TypeDefs", method)
			}
			e.methodOwners[method] = index
		}
	}
	if uint64(len(e.methodOwners)) != uint64(tables.MethodDef.Len()) {
		return fmt.Errorf("MethodDef: rows without a declaring TypeDef")
	}
	for index := range tables.CustomAttribute.Indices() {
		attribute, err := tables.CustomAttribute.At(index)
		if err != nil {
			return err
		}
		e.attributesByParent[attribute.Parent] = append(e.attributesByParent[attribute.Parent], index)
	}
	for index := range tables.Constant.Indices() {
		constant, err := tables.Constant.At(index)
		if err != nil {
			return err
		}
		if _, exists := e.constantsByParent[constant.Parent]; exists {
			return fmt.Errorf("constant[%d]: duplicate constant for %s[%d]", index, constant.Parent.Tag, constant.Parent.Index)
		}
		e.constantsByParent[constant.Parent] = index
	}
	for index := range tables.GenericParam.Indices() {
		param, err := tables.GenericParam.At(index)
		if err != nil {
			return err
		}
		e.genericsByOwner[param.Owner] = append(e.genericsByOwner[param.Owner], index)
	}
	for index := range tables.GenericParamConstraint.Indices() {
		constraint, err := tables.GenericParamConstraint.At(index)
		if err != nil {
			return err
		}
		e.constraintsByParam[constraint.Owner] = append(e.constraintsByParam[constraint.Owner], index)
	}
	owners := make(map[winmd.CodedIndex[winmd.HasSemantics]]winmd.Index)
	for mapping, err := range tables.PropertyMap.All() {
		if err != nil {
			return err
		}
		if _, exists := e.propertiesByType[mapping.Parent]; exists {
			return fmt.Errorf("PropertyMap: duplicate mapping for TypeDef[%d]", mapping.Parent)
		}
		e.propertiesByType[mapping.Parent] = mapping.PropertyList
		for index := range mapping.PropertyList.All() {
			key := winmd.CodedIndex[winmd.HasSemantics]{Tag: winmd.HasSemantics_Property, Index: index}
			if _, exists := owners[key]; exists {
				return fmt.Errorf("property[%d]: multiple declaring TypeDefs", index)
			}
			owners[key] = mapping.Parent
		}
	}
	for mapping, err := range tables.EventMap.All() {
		if err != nil {
			return err
		}
		if _, exists := e.eventsByType[mapping.Parent]; exists {
			return fmt.Errorf("EventMap: duplicate mapping for TypeDef[%d]", mapping.Parent)
		}
		e.eventsByType[mapping.Parent] = mapping.EventList
		for index := range mapping.EventList.All() {
			key := winmd.CodedIndex[winmd.HasSemantics]{Tag: winmd.HasSemantics_Event, Index: index}
			if _, exists := owners[key]; exists {
				return fmt.Errorf("event[%d]: multiple declaring TypeDefs", index)
			}
			owners[key] = mapping.Parent
		}
	}
	if uint64(len(owners)) != uint64(tables.Property.Len())+uint64(tables.Event.Len()) {
		return fmt.Errorf("PropertyMap/EventMap: property or event without a declaring TypeDef")
	}
	for index := range tables.MethodSemantics.Indices() {
		semantics, err := tables.MethodSemantics.At(index)
		if err != nil {
			return err
		}
		owner, ok := owners[semantics.Association]
		methodOwner, methodOK := e.methodOwners[semantics.Method]
		if !ok || !methodOK || owner != methodOwner {
			return fmt.Errorf("MethodSemantics[%d]: MethodDef[%d] and %s[%d] must have the same declaring TypeDef", index, semantics.Method, semantics.Association.Tag, semantics.Association.Index)
		}
		e.semantics[semantics.Association] = append(e.semantics[semantics.Association], index)
		if semantics.Semantics != winmd.MethodSemanticsAttributes_Other {
			e.accessorMethods[semantics.Method] = true
		}
	}
	return nil
}

func selectedMember(access winmd.MemberAccess, includeProtected bool) bool {
	return access == winmd.MemberAccess_Public || includeProtected && (access == winmd.MemberAccess_Family || access == winmd.MemberAccess_FamORAssem)
}

func (e *assemblyExtractor) externallyVisible(index winmd.Index) (bool, error) {
	for range maxTypeDepth {
		typ, err := e.metadata.Tables.TypeDef.At(index)
		if err != nil {
			return false, err
		}
		switch typ.Flags.Visibility() {
		case winmd.TypeVisibility_Public, winmd.TypeVisibility_NestedPublic:
		case winmd.TypeVisibility_NestedFamily, winmd.TypeVisibility_NestedFamORAssem:
			if !e.selected.IncludeProtected {
				return false, nil
			}
		default:
			return false, nil
		}
		parent, nested := e.signatures.parents[index]
		if !nested {
			return true, nil
		}
		index = parent
	}
	return false, fmt.Errorf("TypeDef[%d]: cyclic or excessively nested visibility chain", index)
}

func (e *assemblyExtractor) readType(index winmd.Index, assemblyName, name string) (typeInfo, error) {
	typ, err := e.metadata.Tables.TypeDef.At(index)
	if err != nil {
		return typeInfo{}, err
	}
	info := typeInfo{
		Assembly:     assemblyName,
		Kind:         "class",
		Constructors: make(map[string]methodInfo),
		Methods:      make(map[string]methodInfo),
		Properties:   make(map[string]propertyInfo),
		Events:       make(map[string]eventInfo),
		Fields:       make(map[string]fieldInfo),
		Constants:    make(map[string]fieldInfo),
	}
	if typ.Extends.Tag != winmd.TypeDefOrRef_Null {
		info.BaseType, err = e.signatures.typeDefOrRef(typ.Extends)
		if err != nil {
			return typeInfo{}, fmt.Errorf("base type: %w", err)
		}
	}
	if typ.Flags.Semantics() == winmd.TypeSemantics_Interface {
		info.Kind = "interface"
	} else {
		switch info.BaseType {
		case "System.Enum":
			info.Kind = "enum"
		case "System.ValueType":
			if name != "System.Enum" {
				info.Kind = "struct"
			}
		case "System.MulticastDelegate", "System.Delegate":
			if name != "System.MulticastDelegate" {
				info.Kind = "delegate"
			}
		}
	}
	info.GenericParameters, err = e.genericParameters(winmd.CodedIndex[winmd.TypeOrMethodDef]{Tag: winmd.TypeOrMethodDef_TypeDef, Index: index})
	if err != nil {
		return typeInfo{}, err
	}
	attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_TypeDef, Index: index})
	if err != nil {
		return typeInfo{}, err
	}
	info.Attributes = attrs.attributes
	for row := range typ.MethodList.All() {
		if e.accessorMethods[row] {
			continue
		}
		method, err := e.metadata.Tables.MethodDef.At(row)
		if err != nil {
			return typeInfo{}, err
		}
		methodName := method.Name.String()
		if methodName == ".cctor" || !selectedMember(method.Flags.Access(), e.selected.IncludeProtected) {
			continue
		}
		key, member, err := e.readMethod(row, method)
		if err != nil {
			return typeInfo{}, fmt.Errorf("MethodDef[%d] %q: %w", row, methodName, err)
		}
		destination := info.Methods
		if methodName == ".ctor" {
			destination = info.Constructors
		}
		if _, exists := destination[key]; exists {
			return typeInfo{}, fmt.Errorf("MethodDef[%d]: duplicate canonical method %q", row, key)
		}
		destination[key] = member
	}
	for row := range e.propertiesByType[index].All() {
		key, member, visible, err := e.readProperty(row)
		if err != nil {
			return typeInfo{}, fmt.Errorf("property[%d]: %w", row, err)
		}
		if !visible {
			continue
		}
		if _, exists := info.Properties[key]; exists {
			return typeInfo{}, fmt.Errorf("property[%d]: duplicate canonical property %q", row, key)
		}
		info.Properties[key] = member
	}
	for row := range e.eventsByType[index].All() {
		key, member, visible, err := e.readEvent(row)
		if err != nil {
			return typeInfo{}, fmt.Errorf("event[%d]: %w", row, err)
		}
		if !visible {
			continue
		}
		if _, exists := info.Events[key]; exists {
			return typeInfo{}, fmt.Errorf("event[%d]: duplicate canonical event %q", row, key)
		}
		info.Events[key] = member
	}
	for row := range typ.FieldList.All() {
		field, err := e.metadata.Tables.Field.At(row)
		if err != nil {
			return typeInfo{}, err
		}
		key := field.Name.String()
		if info.Kind == "enum" && key == "value__" || !selectedMember(field.Flags.Access(), e.selected.IncludeProtected) {
			continue
		}
		member, err := e.readField(row, field)
		if err != nil {
			return typeInfo{}, fmt.Errorf("field[%d] %q: %w", row, key, err)
		}
		_, fieldExists := info.Fields[key]
		_, constantExists := info.Constants[key]
		if fieldExists || constantExists {
			return typeInfo{}, fmt.Errorf("field[%d]: duplicate canonical field %q", row, key)
		}
		if field.Flags.HasAll(winmd.FieldFlags_Literal) {
			info.Constants[key] = member
		} else {
			info.Fields[key] = member
		}
	}
	return info, nil
}

func (e *assemblyExtractor) genericParameters(owner winmd.CodedIndex[winmd.TypeOrMethodDef]) ([]genericParameter, error) {
	var result []genericParameter
	numbers := make(map[uint16]bool)
	for _, index := range e.genericsByOwner[owner] {
		param, err := e.metadata.Tables.GenericParam.At(index)
		if err != nil {
			return nil, err
		}
		if numbers[param.Number] {
			return nil, fmt.Errorf("GenericParam[%d]: duplicate parameter number %d for %s[%d]", index, param.Number, owner.Tag, owner.Index)
		}
		numbers[param.Number] = true
		info := genericParameter{Name: param.Name.String(), Number: param.Number, Flags: uint16(param.Flags)}
		for _, row := range e.constraintsByParam[index] {
			constraint, err := e.metadata.Tables.GenericParamConstraint.At(row)
			if err != nil {
				return nil, err
			}
			name, err := e.signatures.typeDefOrRef(constraint.Constraint)
			if err != nil {
				return nil, fmt.Errorf("GenericParam[%d] %q, GenericParamConstraint[%d]: %w", index, info.Name, row, err)
			}
			info.Constraints = append(info.Constraints, name)
		}
		slices.Sort(info.Constraints)
		result = append(result, info)
	}
	slices.SortFunc(result, func(a, b genericParameter) int { return cmp.Compare(a.Number, b.Number) })
	return result, nil
}

type parameterRow struct {
	index winmd.Index
	param winmd.Param
}

func (e *assemblyExtractor) parameterRows(list winmd.Slice, count int) (map[int]parameterRow, error) {
	rows := make(map[int]parameterRow)
	for index := range list.All() {
		param, err := e.metadata.Tables.Param.At(index)
		if err != nil {
			return nil, err
		}
		sequence := int(param.Sequence)
		if sequence > count {
			return nil, fmt.Errorf("param[%d]: sequence %d exceeds signature parameter count %d", index, sequence, count)
		}
		if _, exists := rows[sequence]; exists {
			return nil, fmt.Errorf("param[%d]: duplicate sequence %d", index, sequence)
		}
		rows[sequence] = parameterRow{index: index, param: param}
	}
	return rows, nil
}

func parameterModifier(typ winmd.SigType, flags winmd.ParamAttributes) string {
	if typ.Kind != winmd.ElementType_BYREF {
		return ""
	}
	switch flags & (winmd.ParamAttributes_In | winmd.ParamAttributes_Out) {
	case winmd.ParamAttributes_Out:
		return "out"
	case winmd.ParamAttributes_In:
		return "in"
	default:
		return "ref"
	}
}

func (e *assemblyExtractor) parameters(signature []winmd.SigParam, rows map[int]parameterRow) ([]parameterInfo, error) {
	result := make([]parameterInfo, len(signature))
	for i, param := range signature {
		typ, err := e.signatures.renderType(param.Type, 0)
		if err != nil {
			return nil, fmt.Errorf("signature parameter %d: %w", i+1, err)
		}
		info := parameterInfo{Type: typ}
		row, exists := rows[i+1]
		info.Modifier = parameterModifier(param.Type, row.param.Flags)
		if exists {
			attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_Param, Index: row.index})
			if err != nil {
				return nil, fmt.Errorf("param[%d] %q: %w", row.index, row.param.Name.String(), err)
			}
			info.Attributes = attrs.attributes
		}
		result[i] = info
	}
	return result, nil
}

func (e *assemblyExtractor) readMethod(index winmd.Index, method winmd.MethodDef) (string, methodInfo, error) {
	name := method.Name.String()
	if name == "" {
		return "", methodInfo{}, fmt.Errorf("empty method name")
	}
	sig, err := e.metadata.MethodDefSignature(method.Signature)
	if err != nil {
		return "", methodInfo{}, fmt.Errorf("signature: %w", err)
	}
	if name == ".ctor" && (sig.Generic != 0 || sig.RetType.Type.Kind != winmd.ElementType_VOID || method.Flags.HasAll(winmd.MethodFlags_Static)) {
		return "", methodInfo{}, fmt.Errorf("instance constructor must be nongeneric, nonstatic, and return System.Void")
	}
	info := methodInfo{VarArgs: sig.VarArgs}
	info.ReturnType, err = e.signatures.renderType(sig.RetType.Type, 0)
	if err != nil {
		return "", methodInfo{}, fmt.Errorf("return type: %w", err)
	}
	rows, err := e.parameterRows(method.ParamList, len(sig.Param))
	if err != nil {
		return "", methodInfo{}, err
	}
	info.Parameters, err = e.parameters(sig.Param, rows)
	if err != nil {
		return "", methodInfo{}, err
	}
	if row, exists := rows[0]; exists {
		attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_Param, Index: row.index})
		if err != nil {
			return "", methodInfo{}, fmt.Errorf("return Param[%d]: %w", row.index, err)
		}
		info.ReturnAttributes = attrs.attributes
	}
	info.GenericParameters, err = e.genericParameters(winmd.CodedIndex[winmd.TypeOrMethodDef]{Tag: winmd.TypeOrMethodDef_MethodDef, Index: index})
	if err != nil {
		return "", methodInfo{}, err
	}
	if uint64(len(info.GenericParameters)) != uint64(sig.Generic) {
		return "", methodInfo{}, fmt.Errorf("GenericParam count %d disagrees with signature arity %d", len(info.GenericParameters), sig.Generic)
	}
	for _, param := range info.GenericParameters {
		if uint32(param.Number) >= sig.Generic {
			return "", methodInfo{}, fmt.Errorf("GenericParam number %d exceeds signature arity %d", param.Number, sig.Generic)
		}
	}
	attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_MethodDef, Index: index})
	if err != nil {
		return "", methodInfo{}, err
	}
	info.Attributes = attrs.attributes
	return methodIdentity(name, sig.Generic, info.Parameters, info.ReturnType, sig.VarArgs), info, nil
}

type accessorMethod struct {
	index  winmd.Index
	method winmd.MethodDef
}

type accessorSet struct {
	methods map[winmd.MethodSemanticsAttributes]accessorMethod
	visible bool
	static  bool
}

func (e *assemblyExtractor) readAccessors(parent winmd.CodedIndex[winmd.HasSemantics]) (accessorSet, error) {
	result := accessorSet{methods: make(map[winmd.MethodSemanticsAttributes]accessorMethod)}
	for _, index := range e.semantics[parent] {
		semantics, err := e.metadata.Tables.MethodSemantics.At(index)
		if err != nil {
			return accessorSet{}, err
		}
		if semantics.Semantics == winmd.MethodSemanticsAttributes_Other {
			// Associated helpers remain in Methods; unlike ordinary accessors,
			// their signatures are not represented by a property or event.
			continue
		}
		valid := false
		switch parent.Tag {
		case winmd.HasSemantics_Property:
			valid = semantics.Semantics == winmd.MethodSemanticsAttributes_Getter || semantics.Semantics == winmd.MethodSemanticsAttributes_Setter
		case winmd.HasSemantics_Event:
			valid = semantics.Semantics == winmd.MethodSemanticsAttributes_AddOn || semantics.Semantics == winmd.MethodSemanticsAttributes_RemoveOn || semantics.Semantics == winmd.MethodSemanticsAttributes_Fire
		}
		if !valid {
			return accessorSet{}, fmt.Errorf("MethodSemantics[%d]: unsupported %s semantics %v", index, parent.Tag, semantics.Semantics)
		}
		if _, exists := result.methods[semantics.Semantics]; exists {
			return accessorSet{}, fmt.Errorf("MethodSemantics[%d]: duplicate %v accessor", index, semantics.Semantics)
		}
		method, err := e.metadata.Tables.MethodDef.At(semantics.Method)
		if err != nil {
			return accessorSet{}, err
		}
		static := method.Flags.HasAll(winmd.MethodFlags_Static)
		if len(result.methods) != 0 && result.static != static {
			return accessorSet{}, fmt.Errorf("MethodSemantics[%d]: inconsistent static and instance accessors", index)
		}
		result.static = static
		result.visible = result.visible || selectedMember(method.Flags.Access(), e.selected.IncludeProtected)
		result.methods[semantics.Semantics] = accessorMethod{index: semantics.Method, method: method}
	}
	return result, nil
}

func (e *assemblyExtractor) readProperty(index winmd.Index) (string, propertyInfo, bool, error) {
	accessors, err := e.readAccessors(winmd.CodedIndex[winmd.HasSemantics]{Tag: winmd.HasSemantics_Property, Index: index})
	if err != nil || !accessors.visible {
		return "", propertyInfo{}, false, err
	}
	property, err := e.metadata.Tables.Property.At(index)
	if err != nil {
		return "", propertyInfo{}, false, err
	}
	name := property.Name.String()
	if name == "" {
		return "", propertyInfo{}, false, fmt.Errorf("empty property name")
	}
	sig, err := e.metadata.PropertySignature(property.Type)
	if err != nil {
		return "", propertyInfo{}, false, fmt.Errorf("%q signature: %w", name, err)
	}
	if sig.HasThis == accessors.static {
		return "", propertyInfo{}, false, fmt.Errorf("%q signature and accessor instance flags disagree", name)
	}
	var info propertyInfo
	info.Type, err = e.signatures.renderType(sig.Type, 0)
	if err != nil {
		return "", propertyInfo{}, false, fmt.Errorf("%q type: %w", name, err)
	}
	var rows map[int]parameterRow
	if getter, exists := accessors.methods[winmd.MethodSemanticsAttributes_Getter]; exists {
		getterSig, err := e.metadata.MethodDefSignature(getter.method.Signature)
		if err != nil {
			return "", propertyInfo{}, false, fmt.Errorf("%q getter MethodDef[%d] signature: %w", name, getter.index, err)
		}
		if len(getterSig.Param) != len(sig.Param) {
			return "", propertyInfo{}, false, fmt.Errorf("%q getter MethodDef[%d] parameter count disagrees with property signature", name, getter.index)
		}
		rows, err = e.parameterRows(getter.method.ParamList, len(getterSig.Param))
		if err != nil {
			return "", propertyInfo{}, false, fmt.Errorf("%q getter MethodDef[%d]: %w", name, getter.index, err)
		}
	}
	if setter, exists := accessors.methods[winmd.MethodSemanticsAttributes_Setter]; exists {
		setterSig, err := e.metadata.MethodDefSignature(setter.method.Signature)
		if err != nil {
			return "", propertyInfo{}, false, fmt.Errorf("%q setter MethodDef[%d] signature: %w", name, setter.index, err)
		}
		if len(setterSig.Param) != len(sig.Param)+1 {
			return "", propertyInfo{}, false, fmt.Errorf("%q setter MethodDef[%d] parameter count disagrees with property signature", name, setter.index)
		}
		setterRows, err := e.parameterRows(setter.method.ParamList, len(setterSig.Param))
		if err != nil {
			return "", propertyInfo{}, false, fmt.Errorf("%q setter MethodDef[%d]: %w", name, setter.index, err)
		}
		if rows == nil {
			rows = setterRows
		}
	}
	info.Parameters, err = e.parameters(sig.Param, rows)
	if err != nil {
		return "", propertyInfo{}, false, fmt.Errorf("%q: %w", name, err)
	}
	attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_Property, Index: index})
	if err != nil {
		return "", propertyInfo{}, false, fmt.Errorf("%q: %w", name, err)
	}
	info.Attributes = attrs.attributes
	return propertyIdentity(name, info.Parameters, info.Type), info, true, nil
}

func (e *assemblyExtractor) readEvent(index winmd.Index) (string, eventInfo, bool, error) {
	accessors, err := e.readAccessors(winmd.CodedIndex[winmd.HasSemantics]{Tag: winmd.HasSemantics_Event, Index: index})
	if err != nil || !accessors.visible {
		return "", eventInfo{}, false, err
	}
	event, err := e.metadata.Tables.Event.At(index)
	if err != nil {
		return "", eventInfo{}, false, err
	}
	name := event.Name.String()
	if name == "" {
		return "", eventInfo{}, false, fmt.Errorf("empty event name")
	}
	var info eventInfo
	info.Type, err = e.signatures.typeDefOrRef(event.EventType)
	if err != nil {
		return "", eventInfo{}, false, fmt.Errorf("%q type: %w", name, err)
	}
	attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_Event, Index: index})
	if err != nil {
		return "", eventInfo{}, false, fmt.Errorf("%q: %w", name, err)
	}
	info.Attributes = attrs.attributes
	return name, info, true, nil
}

func (e *assemblyExtractor) readField(index winmd.Index, field winmd.Field) (fieldInfo, error) {
	if field.Name.String() == "" {
		return fieldInfo{}, fmt.Errorf("empty field name")
	}
	sig, err := e.metadata.FieldSignature(field.Signature)
	if err != nil {
		return fieldInfo{}, fmt.Errorf("signature: %w", err)
	}
	var info fieldInfo
	info.Type, err = e.signatures.renderType(sig.Type, 0)
	if err != nil {
		return fieldInfo{}, err
	}
	_, hasConstant := e.constantsByParent[winmd.CodedIndex[winmd.HasConstant]{Tag: winmd.HasConstant_Field, Index: index}]
	if field.Flags.HasAll(winmd.FieldFlags_Literal) && !hasConstant {
		return fieldInfo{}, fmt.Errorf("literal field has no Constant row")
	}
	attrs, err := e.readAttributes(winmd.CodedIndex[winmd.HasCustomAttribute]{Tag: winmd.HasCustomAttribute_Field, Index: index})
	if err != nil {
		return fieldInfo{}, err
	}
	info.Attributes = attrs.attributes
	return info, nil
}

type forwardedType struct {
	name      string
	namespace string
	assembly  string
	depth     int
}

func (e *assemblyExtractor) forwardedTypes() (map[string]string, error) {
	cache := make(map[winmd.Index]forwardedType)
	active := make(map[winmd.Index]bool)
	var resolve func(winmd.Index, int) (forwardedType, error)
	resolve = func(index winmd.Index, depth int) (forwardedType, error) {
		if depth >= maxTypeDepth || active[index] {
			return forwardedType{}, fmt.Errorf("ExportedType[%d]: cyclic or excessively nested forwarder", index)
		}
		if typ, exists := cache[index]; exists {
			if depth+typ.depth > maxTypeDepth {
				return forwardedType{}, fmt.Errorf("ExportedType[%d]: forwarder nesting limit exceeded", index)
			}
			return typ, nil
		}
		active[index] = true
		defer delete(active, index)
		exported, err := e.metadata.Tables.ExportedType.At(index)
		if err != nil {
			return forwardedType{}, err
		}
		name := exported.Name.String()
		if name == "" {
			return forwardedType{}, fmt.Errorf("ExportedType[%d]: empty type name", index)
		}
		typ := forwardedType{name: name, namespace: exported.Namespace.String(), depth: 1}
		switch exported.Implementation.Tag {
		case winmd.Implementation_AssemblyRef:
			if !exported.Flags.HasAll(winmd.TypeFlags_IsTypeForwarder) {
				return forwardedType{}, fmt.Errorf("ExportedType[%d] %q: AssemblyRef target without IsTypeForwarder", index, name)
			}
			assembly, err := e.metadata.Tables.AssemblyRef.At(exported.Implementation.Index)
			if err != nil {
				return forwardedType{}, err
			}
			typ.assembly = assembly.Name.String()
			if typ.assembly == "" {
				return forwardedType{}, fmt.Errorf("ExportedType[%d]: target AssemblyRef[%d] has an empty name", index, exported.Implementation.Index)
			}
			if typ.namespace != "" {
				typ.name = typ.namespace + "." + name
			}
		case winmd.Implementation_ExportedType:
			parent, err := resolve(exported.Implementation.Index, depth+1)
			if err != nil {
				return forwardedType{}, err
			}
			typ.name = parent.name + "+" + name
			typ.namespace, typ.assembly, typ.depth = parent.namespace, parent.assembly, parent.depth+1
		case winmd.Implementation_File:
			return forwardedType{}, fmt.Errorf("ExportedType[%d] %q: multi-module exports (File targets) are not supported", index, name)
		default:
			return forwardedType{}, fmt.Errorf("ExportedType[%d] %q: unsupported implementation %v", index, name, exported.Implementation.Tag)
		}
		cache[index] = typ
		return typ, nil
	}
	result := make(map[string]string)
	names := make(map[string]winmd.Index)
	for index := range e.metadata.Tables.ExportedType.Indices() {
		typ, err := resolve(index, 0)
		if err != nil {
			return nil, err
		}
		if previous, exists := names[typ.name]; exists {
			return nil, fmt.Errorf("ExportedType[%d] and ExportedType[%d]: duplicate canonical forwarder %q", previous, index, typ.name)
		}
		names[typ.name] = index
		if includesNamespace(typ.namespace, e.selected) {
			result[typ.name] = typ.assembly
		}
	}
	return result, nil
}
