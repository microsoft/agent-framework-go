// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Names are presentation only. Every index retains all canonical identities;
// neither an overload nor an external type is evidence for resolving ambiguity.
type dotnetNames struct {
	inv          declarationInventory
	full, suffix map[string][]string
	namespaces   map[string]bool
	classes      map[string]int // 1: reference, -1: value, absent: unknown
	members      map[dnGroup]map[string]declarationMethod
	labels       map[dnGroup]map[string]string
}

type (
	dnGroup struct{ owner, kind string }
	dnType  struct {
		name string
		args []dnType
	}
)

type dnSignature struct {
	name   dnType
	params []string
	result string
	call   bool
}
type dnScope struct {
	owner string
	vars  map[string]string // source names -> slots, slots -> display names
	kinds map[string]int
}

var dnPrimitives = map[string]string{
	"System.Void": "void", "System.Boolean": "bool", "System.Char": "char",
	"System.SByte": "sbyte", "System.Byte": "byte", "System.Int16": "short", "System.UInt16": "ushort",
	"System.Int32": "int", "System.UInt32": "uint", "System.Int64": "long", "System.UInt64": "ulong",
	"System.Single": "float", "System.Double": "double", "System.Decimal": "decimal",
	"System.IntPtr": "nint", "System.UIntPtr": "nuint", "System.String": "string", "System.Object": "object",
}

var (
	dnIdentifier = regexp.MustCompile(`^[\p{L}_][\p{L}\p{Nd}_]*$`)
	dnToken      = regexp.MustCompile("[\\p{L}_][\\p{L}\\p{Nd}_]*(?:`[1-9][0-9]*)?(?:[.+][\\p{L}_][\\p{L}\\p{Nd}_]*(?:`[1-9][0-9]*)?)*")
)

func newDotnetNames(inv declarationInventory) *dotnetNames {
	n := &dotnetNames{
		inv: inv, full: map[string][]string{}, suffix: map[string][]string{},
		namespaces: map[string]bool{}, classes: map[string]int{}, members: map[dnGroup]map[string]declarationMethod{}, labels: map[dnGroup]map[string]string{},
	}
	known := map[string]bool{}
	collect := func(text string) {
		// Lex named atoms even in opaque fnptr/custom-modifier signatures. Never
		// split an entire signature on dots or commas to discover type names.
		for _, name := range dnToken.FindAllString(text, -1) {
			switch name {
			case "fnptr", "modreq", "modopt", "default", "cdecl", "stdcall", "thiscall", "fastcall", "vararg", "hasthis", "explicitthis":
				continue
			}
			known[name] = true
		}
	}
	generics := func(gs []declarationGeneric) {
		for _, g := range gs {
			for _, constraint := range g.Constraints {
				collect(constraint)
			}
		}
	}
	for name := range dnPrimitives {
		known[name], n.classes[name] = true, -1
	}
	for _, name := range []string{"System.String", "System.Object", "System.Type", "System.Text.Json.JsonSerializerOptions", "System.IServiceProvider", "Microsoft.Extensions.Logging.ILoggerFactory"} {
		n.classes[name] = 1
	}
	for owner, t := range inv.Types {
		known[owner] = true
		collect(t.BaseType)
		generics(t.GenericParameters)
		switch t.Kind {
		case "struct", "enum":
			n.classes[dnDots(owner)] = -1
		case "class", "interface", "delegate":
			n.classes[dnDots(owner)] = 1
		}
		groups := map[string]map[string]declarationMethod{
			"method": t.Methods, "constructor": t.Constructors,
			"property": {}, "event": {}, "field": {}, "constant": {},
		}
		for key, p := range t.Properties {
			groups["property"][key] = declarationMethod{Parameters: p.Parameters, ReturnType: p.Type, ReturnAttributes: p.Attributes}
		}
		for key, e := range t.Events {
			groups["event"][key] = declarationMethod{ReturnType: e.Type, ReturnAttributes: e.Attributes}
		}
		for kind, fields := range map[string]map[string]declarationField{"field": t.Fields, "constant": t.Constants} {
			for key, f := range fields {
				groups[kind][key] = declarationMethod{ReturnType: f.Type, ReturnAttributes: f.Attributes}
			}
		}
		for kind, members := range groups {
			n.members[dnGroup{owner, kind}] = members
			for _, m := range members {
				collect(m.ReturnType)
				generics(m.GenericParameters)
				for _, p := range m.Parameters {
					collect(p.Type)
				}
			}
		}
	}
	for name := range known {
		full := dnDots(name)
		namespace, _ := dnOwner(name)
		n.namespaces[namespace] = true
		if name == "System.Nullable`1" || strings.HasPrefix(name, "System.ValueTuple`") {
			n.classes[full] = -1
		}
		n.full[full] = append(n.full[full], name)
		parts := strings.Split(full, ".") // A lexed named atom, not a signature.
		for i := range parts {
			suffix := strings.Join(parts[i:], ".")
			n.suffix[suffix] = append(n.suffix[suffix], name)
		}
	}
	for _, index := range []map[string][]string{n.full, n.suffix} {
		for key := range index {
			slices.Sort(index[key])
		}
	}
	return n
}

func dnDots(s string) string { return strings.ReplaceAll(s, "+", ".") }

func dnOwner(name string) (string, string) {
	outer, _, _ := strings.Cut(name, "+")
	i := strings.LastIndexByte(outer, '.')
	return name[:max(i, 0)], dnDots(name[i+1:])
}

// dnSplit recognizes separators only outside balanced type/call delimiters.
func dnSplit(s, separators string) ([]string, bool) {
	var parts []string
	var stack []byte
	start := 0
	for i := 0; i < len(s); i++ {
		width := 0
		if strings.HasPrefix(s[i:], "->") {
			if separators == "->" && len(stack) == 0 {
				width = 2
			} else {
				i++
				continue
			}
		} else if len(stack) == 0 && separators != "->" && strings.ContainsRune(separators, rune(s[i])) {
			width = 1
		}
		if width != 0 {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			i += width - 1
			start = i + 1
			continue
		}
		if j := strings.IndexByte("<([", s[i]); j >= 0 {
			stack = append(stack, ">)]"[j])
		} else if strings.ContainsRune(">)]", rune(s[i])) {
			if len(stack) == 0 || stack[len(stack)-1] != s[i] {
				return nil, false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return append(parts, strings.TrimSpace(s[start:])), len(stack) == 0
}

func dnAtom(s string) (string, int, bool) {
	name, count, generic := strings.Cut(s, "`")
	arity, err := strconv.Atoi(count)
	return name, arity, dnIdentifier.MatchString(name) && (!generic || err == nil && arity > 0 && arity <= 65535)
}

// The small expression grammar deliberately excludes custom modifiers, fnptr,
// and bounded arrays. Those remain exact canonical identities, not guesses.
func dnParse(s string, depth int) (t dnType, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || depth >= 64 {
		return t, false
	}
	if _, balanced := dnSplit(s, ""); !balanced {
		return t, false
	}
	last := s[len(s)-1]
	if strings.ContainsRune("?*&", rune(last)) || last == ']' {
		cut := len(s) - 1
		if last == ']' {
			cut = strings.LastIndexByte(s, '[')
			if cut < 0 || strings.Trim(s[cut+1:len(s)-1], ",:*") != "" {
				return t, false
			}
		}
		child, valid := dnParse(s[:cut], depth+1)
		shape := s[cut:]
		if last == '?' && (child.name == "?" || child.name == "&" || child.name == "*") {
			return t, false
		}
		if last == ']' && strings.Contains(shape, ":") {
			if shape == "[:]" {
				shape = "[*]"
			} else if dimensions := strings.Split(shape[1:len(shape)-1], ","); !slices.ContainsFunc(dimensions, func(d string) bool { return d != ":" }) {
				shape = "[" + strings.Repeat(",", len(dimensions)-1) + "]"
			}
		}
		return dnType{shape, []dnType{child}}, valid
	}
	if strings.HasPrefix(s, "!") {
		_, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(s, "!"), "!"), 10, 16)
		return dnType{name: s}, err == nil
	}
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		items, valid := dnSplit(s[1:len(s)-1], ",")
		if !valid || len(items) < 2 || len(items) > 7 {
			return t, false
		}
		for _, item := range items {
			child, valid := dnParse(item, depth+1)
			if !valid {
				if i := strings.LastIndexAny(item, " \t"); i > 0 && dnIdentifier.MatchString(item[i+1:]) {
					child, valid = dnParse(item[:i], depth+1)
				}
			}
			if !valid {
				return t, false
			}
			t.args = append(t.args, child)
		}
		t.name = "System.ValueTuple`" + strconv.Itoa(len(items))
		return t, true
	}
	absolute := strings.HasPrefix(s, "global::")
	parts, valid := dnSplit(strings.TrimPrefix(s, "global::"), ".+")
	// CLR nested instantiations bind one argument list to the whole owner.
	// Source dotted paths infer arity independently on each instantiated part.
	nested, _ := dnSplit(s, "+")
	clrNested := len(nested) > 1
	arity := 0
	for i, part := range parts {
		base := part
		if open := strings.IndexByte(part, '<'); open >= 0 {
			if !strings.HasSuffix(part, ">") || clrNested && i != len(parts)-1 {
				return t, false
			}
			base = part[:open]
			items, balanced := dnSplit(part[open+1:len(part)-1], ",")
			if !balanced {
				return t, false
			}
			for _, item := range items {
				child, parsed := dnParse(item, depth+1)
				if !parsed {
					return t, false
				}
				t.args = append(t.args, child)
			}
			if !clrNested && !strings.Contains(base, "`") {
				base += "`" + strconv.Itoa(len(items))
			}
		}
		_, count, parsed := dnAtom(base)
		valid = valid && parsed
		arity += count
		parts[i] = base
	}
	t.name = strings.Join(parts, ".")
	if absolute {
		t.name = "global::" + t.name
	}
	return t, valid && (len(t.args) == 0 || len(t.args) == arity)
}

func dnExpand(name string, args []string) (string, bool) {
	parts, used := strings.Split(dnDots(name), "."), 0
	for i, part := range parts {
		base, count, ok := dnAtom(part)
		if !ok || count > len(args)-used {
			return name, false
		}
		if count != 0 {
			parts[i] = base + "<" + strings.Join(args[used:used+count], ", ") + ">"
			used += count
		}
	}
	return strings.Join(parts, "."), used == len(args)
}

func dnGenericLabels(gs []declarationGeneric) []string {
	names := make([]string, len(gs))
	for _, g := range gs {
		if int(g.Number) >= len(names) || names[g.Number] != "" || !dnIdentifier.MatchString(g.Name) || slices.Contains(names, g.Name) {
			return nil
		}
		names[g.Number] = g.Name
	}
	return names
}

func (n *dotnetNames) typeLabel(canonical string) (namespace, name string) {
	namespace, name = dnOwner(canonical)
	if expanded, ok := dnExpand(name, dnGenericLabels(n.inv.Types[canonical].GenericParameters)); ok {
		name = expanded
	}
	return namespace, name
}

func (n *dotnetNames) lookup(name, owner string) []string {
	for canonical, alias := range dnPrimitives {
		if name == alias {
			return n.full[canonical]
		}
	}
	if strings.HasPrefix(name, "global::") {
		return n.full[strings.TrimPrefix(name, "global::")]
	}
	if exact := n.full[name]; len(exact) != 0 && strings.Contains(name, ".") {
		return exact
	}
	for prefix := name; strings.Contains(prefix, "."); {
		prefix = prefix[:strings.LastIndexByte(prefix, '.')]
		if n.namespaces[prefix] {
			return nil // A known namespace qualifier is not an arbitrary suffix.
		}
	}
	candidates := n.suffix[name]
	if len(candidates) > 1 && owner != "" {
		namespace, _ := dnOwner(owner)
		for scope := owner; ; {
			prefix := scope + "+"
			if scope == namespace {
				prefix = scope + "."
				if scope == "" {
					prefix = ""
				}
			}
			var local []string
			for _, candidate := range candidates {
				if rest, found := strings.CutPrefix(candidate, prefix); found && dnDots(rest) == name {
					local = append(local, candidate)
				}
			}
			if len(local) != 0 {
				return local
			}
			if scope == namespace {
				break
			}
			if i := strings.LastIndexByte(scope, '+'); i >= 0 {
				scope = scope[:i]
			} else {
				scope = namespace
			}
		}
	}
	return candidates
}

// resolveOwner accepts only exact declaring paths, never suffix suggestions.
func (n *dotnetNames) resolveOwner(source string) []string {
	if _, exact := n.inv.Types[source]; exact {
		return []string{source}
	}
	t, ok := dnParse(source, 0)
	if !ok {
		return nil
	}
	candidates := n.full[strings.TrimPrefix(t.name, "global::")]
	for _, name := range candidates {
		if _, defined := n.inv.Types[name]; !defined {
			return nil // External homonyms cannot select a declaring definition.
		}
	}
	return slices.Clone(candidates)
}

func (n *dotnetNames) resolveType(source string) []string {
	if _, exact := n.inv.Types[source]; exact && strings.Contains(source, "+") {
		return []string{source}
	}
	t, ok := dnParse(source, 0)
	if !ok {
		return nil
	}
	candidates := n.lookup(t.name, "")
	for _, name := range candidates {
		if _, defined := n.inv.Types[name]; !defined {
			return nil // An external homonym must not make a definition look unique.
		}
	}
	return slices.Clone(candidates)
}

func dnBind(scope *dnScope, gs []declarationGeneric, args []dnType, prefix string) bool {
	if len(args) != 0 && len(args) != len(gs) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range gs {
		if int(g.Number) >= len(gs) {
			return false
		}
		name, slot := g.Name, prefix+strconv.Itoa(int(g.Number))
		if len(args) != 0 {
			name = args[g.Number].name
			if len(args[g.Number].args) != 0 {
				return false
			}
		}
		if !dnIdentifier.MatchString(name) || seen[name] || scope.vars[slot] != "" {
			return false
		}
		seen[name], scope.vars[name], scope.vars[slot], scope.kinds[slot] = true, slot, name, 1
		if g.Flags&8 != 0 { // NotNullableValueTypeConstraint: T? means Nullable<T>.
			scope.kinds[slot] = -1
		}
	}
	return true
}

func (n *dotnetNames) render(t dnType, scope dnScope) (string, bool) {
	if t.name == "" {
		return "", false
	}
	if strings.HasPrefix(t.name, "!") {
		if name := scope.vars[t.name]; name != "" && scope.vars[name] == t.name {
			return name, true
		}
		return t.name, true
	}
	args := make([]string, len(t.args))
	for i, arg := range t.args {
		var ok bool
		if args[i], ok = n.render(arg, scope); !ok {
			return "", false
		}
	}
	if len(args) == 1 && strings.ContainsAny(t.name[:1], "[?*&") {
		return args[0] + t.name, true
	}
	identities := n.full[t.name]
	if len(identities) != 1 {
		return "", false
	}
	if t.name == "System.Nullable`1" && len(args) == 1 {
		return args[0] + "?", true
	}
	if alias := dnPrimitives[t.name]; alias != "" && len(args) == 0 && scope.vars[alias] == "" {
		return alias, true
	}
	_, nested := dnOwner(identities[0])
	parts, name := strings.Split(t.name, "."), t.name
	// Keep the declaring path of nested types even when the leaf is unique.
	for i := len(parts) - len(strings.Split(nested, ".")); i >= 0; i-- {
		if suffix := strings.Join(parts[i:], "."); len(n.suffix[suffix]) == 1 && scope.vars[suffix] == "" {
			name = suffix
			break
		}
	}
	return dnExpand(name, args)
}

func dnReadSignature(s string) (sig dnSignature, ok bool) {
	parts, balanced := dnSplit(s, "->")
	if !balanced || len(parts) > 2 {
		return sig, false
	}
	head := parts[0]
	if len(parts) == 2 {
		sig.result = parts[1]
		if sig.result == "" {
			return sig, false
		}
	}
	if i := strings.IndexByte(head, '('); i >= 0 {
		if !strings.HasSuffix(head, ")") {
			return sig, false
		}
		if params := head[i+1 : len(head)-1]; strings.TrimSpace(params) != "" {
			sig.params, balanced = dnSplit(params, ",")
		}
		head, sig.call = strings.TrimSpace(head[:i]), true
	}
	if head == ".ctor" || head == "<Clone>$" {
		sig.name = dnType{name: head}
		return sig, balanced
	}
	sig.name, ok = dnParse(strings.ReplaceAll(head, "``", "`"), 0)
	return sig, ok && balanced
}

func dnParameter(text, modifier string) (dnType, string, bool) {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"ref", "out", "in"} {
		if strings.HasPrefix(text, prefix+" ") {
			text, modifier = strings.TrimSpace(text[len(prefix)+1:]), prefix
			break
		}
	}
	t, ok := dnParse(text, 0)
	if modifier != "" && t.name != "&" {
		t = dnType{"&", []dnType{t}}
	}
	return t, modifier, ok && slices.Contains([]string{"", "ref", "out", "in"}, modifier)
}

func dnConstructor(owner string) string {
	_, name := dnOwner(owner)
	name = name[strings.LastIndexByte(name, '.')+1:]
	name, _, _ = strings.Cut(name, "`")
	return name
}

func (n *dotnetNames) label(group dnGroup, key string, m declarationMethod) (string, string) {
	sig, ok := dnReadSignature(key)
	scope := dnScope{group.owner, map[string]string{}, map[string]int{}}
	if !ok || !dnBind(&scope, n.inv.Types[group.owner].GenericParameters, nil, "!") || !dnBind(&scope, m.GenericParameters, nil, "!!") {
		return key, ""
	}
	name, ok := dnExpand(sig.name.name, dnGenericLabels(m.GenericParameters))
	if group.kind == "method" && sig.name.name == "<Clone>$" && len(m.GenericParameters) == 0 {
		name, ok = sig.name.name, true
	}
	if group.kind == "constructor" && sig.name.name == ".ctor" {
		name, ok = dnConstructor(group.owner), true
	}
	var params []string
	for _, p := range m.Parameters {
		t, modifier, parsed := dnParameter(p.Type, p.Modifier)
		if t.name == "&" {
			t = t.args[0]
			if modifier == "" {
				modifier = "ref"
			}
		}
		text, rendered := n.render(t, scope)
		ok = ok && parsed && rendered
		if modifier != "" {
			text = modifier + " " + text
		}
		params = append(params, text)
	}
	if m.VarArgs {
		params = append(params, "...")
	}
	if sig.call {
		name += "(" + strings.Join(params, ", ") + ")"
	}
	t, parsed := dnParse(m.ReturnType, 0)
	result, rendered := n.render(t, scope)
	if !ok || !parsed || !rendered {
		return key, ""
	}
	return name, result
}

func (n *dotnetNames) memberLabel(owner, kind, canonicalKey string) string {
	group := dnGroup{owner, kind}
	if n.labels[group] == nil {
		labels := map[string]string{}
		for key, m := range n.members[group] {
			label, result := n.label(group, key, m)
			matches := n.resolveMember(owner, kind, "", label)
			// Check semantic uniqueness, too: M<T>(T) and M<U>(U) do not
			// identify different overloads merely because parameter names differ.
			if len(matches) > 1 && result != "" {
				label += " -> " + result
				matches = n.resolveMember(owner, kind, "", label)
			}
			if len(matches) != 1 || matches[0] != key {
				label = key
			}
			labels[key] = label
		}
		n.labels[group] = labels
	}
	if label, ok := n.labels[group][canonicalKey]; ok {
		return label
	}
	return canonicalKey
}

func (n *dotnetNames) nullableReferences(root *dnType, scope dnScope, flags []int) map[*dnType]bool {
	references := map[*dnType]bool{}
	type slot struct {
		node     *dnType
		kind     int
		optional bool
	}
	var slots []slot
	var visit func(*dnType, bool)
	visit = func(t *dnType, value bool) {
		if t.name == "&" {
			visit(&t.args[0], value)
			return
		}
		if t.name == "System.Nullable`1" && len(t.args) == 1 {
			// Nullable<T> omits its own slot; its argument must be a value type.
			visit(&t.args[0], true)
			return
		}
		kind := n.classes[t.name]
		parameter := strings.HasPrefix(t.name, "!")
		if parameter {
			kind = scope.kinds[t.name]
		} else if strings.HasPrefix(t.name, "[") {
			kind = 1
		}
		if value {
			kind = -1
		}
		references[t] = kind == 1
		generic := len(t.args) != 0 || strings.Contains(t.name, "`")
		if t.name == "*" || t.name == "?" {
			flags = nil // Do not infer unsupported transform layouts.
		} else if kind != -1 || generic || parameter {
			slots = append(slots, slot{t, kind, kind == 0 && !generic && !parameter})
		}
		for i := range t.args {
			visit(&t.args[i], false)
		}
	}
	visit(root, false)
	if len(flags) > len(slots) {
		return references
	}
	// Unknown nongeneric values may omit a slot. Only a full-length vector
	// proves that every such node consumes one; stop at ambiguity otherwise.
	// Never expand a compiler-compressed single flag to infer unknown arguments.
	var annotated []*dnType
	for i, s := range slots {
		if i >= len(flags) || s.optional && len(flags) != len(slots) {
			break
		}
		flag := flags[i]
		if flag < 0 || flag > 2 || s.kind == -1 && flag != 0 {
			return references
		}
		if s.kind == 0 && flag != 0 {
			annotated = append(annotated, s.node)
		}
	}
	for _, node := range annotated {
		references[node] = true
	}
	return references
}

func (n *dotnetNames) same(source, canonical dnType, scope dnScope, flags []int) bool {
	return n.sameType(source, &canonical, scope, n.nullableReferences(&canonical, scope, flags))
}

func (n *dotnetNames) sameType(source dnType, canonical *dnType, scope dnScope, references map[*dnType]bool) bool {
	if source.name == "?" {
		if canonical.name == "System.Nullable`1" && len(canonical.args) == 1 {
			return n.sameType(source.args[0], &canonical.args[0], scope, references)
		}
		// NullableContext alone cannot prove that an unknown type is a reference.
		return references[canonical] && n.sameType(source.args[0], canonical, scope, references)
	}
	if slot := scope.vars[source.name]; slot != "" && !strings.HasPrefix(source.name, "!") {
		source.name = slot
	}
	if strings.ContainsAny(source.name[:1], "![*&") {
		if source.name != canonical.name {
			return false
		}
	} else if names := n.lookup(source.name, scope.owner); len(names) != 1 || dnDots(names[0]) != canonical.name {
		return false
	}
	if len(source.args) != len(canonical.args) {
		return false
	}
	for i := range source.args {
		if !n.sameType(source.args[i], &canonical.args[i], scope, references) {
			return false
		}
	}
	return true
}

func (n *dotnetNames) resolveMember(owner, kind, sourceType, member string) []string {
	members := n.members[dnGroup{owner, kind}]
	var enclosing dnType
	if sourceType != "" {
		var ok bool
		enclosing, ok = dnParse(sourceType, 0)
		if types := n.resolveOwner(sourceType); !ok || len(types) != 1 || types[0] != owner {
			return nil
		}
	}
	if _, exact := members[member]; exact {
		return []string{member}
	}
	sig, ok := dnReadSignature(member)
	if !ok {
		return nil
	}
	var matches []string
	for key, m := range members {
		decl, valid := dnReadSignature(key)
		name := sig.name.name
		if kind == "constructor" && name == dnConstructor(owner) {
			name = ".ctor"
		}
		params := sig.params
		varargs := len(params) != 0 && params[len(params)-1] == "..."
		if varargs {
			params = params[:len(params)-1]
		}
		if !valid || name != decl.name.name || sig.call != decl.call || varargs != m.VarArgs || len(params) != len(m.Parameters) {
			continue
		}
		scope := dnScope{owner, map[string]string{}, map[string]int{}}
		if !dnBind(&scope, n.inv.Types[owner].GenericParameters, enclosing.args, "!") || !dnBind(&scope, m.GenericParameters, sig.name.args, "!!") {
			continue
		}
		for i, text := range params {
			p := m.Parameters[i]
			s, modifier, parsed := dnParameter(text, "")
			c, expected, canonical := dnParameter(p.Type, p.Modifier)
			if c.name == "&" && expected == "" {
				expected = "ref"
			}
			if !parsed || !canonical || modifier != "" && modifier != expected || !n.same(s, c, scope, p.Attributes.NullableFlags) {
				valid = false
				break
			}
		}
		if valid && sig.result != "" {
			s, parsed := dnParse(sig.result, 0)
			c, canonical := dnParse(m.ReturnType, 0)
			valid = parsed && canonical && n.same(s, c, scope, m.ReturnAttributes.NullableFlags)
		}
		if valid {
			matches = append(matches, key)
		}
	}
	slices.Sort(matches)
	return matches
}
