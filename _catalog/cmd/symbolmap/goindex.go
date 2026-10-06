// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// goInventory describes one build configuration, not the union of all platforms.
// Packages and symbol keys use module-relative import paths. The root package
// uses the module's last path element, consistently with signature qualifiers.
// encoding/json sorts the string keys of Symbols when encoding the inventory.
type goInventory struct {
	Module     string              `json:"module"`
	Commit     string              `json:"commit,omitempty"`
	Dirty      *bool               `json:"dirty,omitempty"`
	GOOS       string              `json:"goos"`
	GOARCH     string              `json:"goarch"`
	GoVersion  string              `json:"go_version"`
	CGOEnabled string              `json:"cgo_enabled"`
	BuildTags  string              `json:"build_tags,omitempty"`
	Packages   []string            `json:"packages"`
	Symbols    map[string]goSymbol `json:"symbols"`
	Page       *pageInfo           `json:"page,omitempty"`

	packages map[string]*types.Package
}

type goSymbol struct {
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
}

// indexGo indexes exported declarations in the requested module directory root.
// It may compile packages for export data, but never runs their initializers,
// executables, or tests. The toolchain and dependencies must already be local.
// Nonempty tags overrides GOFLAGS' -tags; otherwise inherited tags are retained.
func indexGo(root string, patterns []string, tags string) (goInventory, error) {
	dir, err := filepath.Abs(root)
	if err != nil {
		return goInventory{}, fmt.Errorf("index Go root %q: %w", root, err)
	}
	rootInfo, err := os.Stat(dir)
	if err != nil {
		return goInventory{}, fmt.Errorf("index Go root %q: %w", dir, err)
	}
	if !rootInfo.IsDir() {
		return goInventory{}, fmt.Errorf("index Go root %q is not a directory", dir)
	}
	if len(patterns) == 0 {
		patterns = []string{"./agent/...", "./message/...", "./tool/...", "./provider/...", "./workflow/..."}
	}

	// GONOPROXY must not let private modules bypass GOPROXY=off. Disable
	// external package drivers as well as toolchain downloads.
	env := goIndexEnv(os.Environ(),
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GONOPROXY=none", "GOVCS=*:off", "GOPACKAGESDRIVER=off",
	)
	data, err := goIndexOutput(dir, env, "go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOVERSION", "GOFLAGS", "GOCACHEPROG")
	if err != nil {
		return goInventory{}, fmt.Errorf("read Go build environment: %w", err)
	}
	var buildEnv struct {
		GOOS        string
		GOARCH      string
		CGOEnabled  string `json:"CGO_ENABLED"`
		GoVersion   string `json:"GOVERSION"`
		GOFLAGS     string
		GOCACHEPROG string
	}
	if err := json.Unmarshal(data, &buildEnv); err != nil {
		return goInventory{}, fmt.Errorf("decode Go build environment: %w", err)
	}
	// An empty environment override would still allow a cache program saved
	// with go env -w. Check the effective setting before loading any packages.
	if buildEnv.GOCACHEPROG != "" {
		return goInventory{}, errors.New("GOCACHEPROG is not supported: Go indexing must not execute custom cache programs")
	}
	buildTags, err := goIndexBuildTags(buildEnv.GOFLAGS, tags)
	if err != nil {
		return goInventory{}, err
	}
	buildFlags := []string{"-mod=readonly"}
	if tags != "" {
		buildFlags = append(buildFlags, "-tags="+tags)
	}
	pkgs, err := packages.Load(&packages.Config{
		Mode:       packages.LoadTypes | packages.NeedDeps | packages.NeedModule,
		Dir:        dir,
		Env:        env,
		BuildFlags: buildFlags,
		Tests:      false,
	}, patterns...)
	if err != nil {
		return goInventory{}, fmt.Errorf("load Go packages (offline, -mod=readonly): %w", err)
	}
	if len(pkgs) == 0 {
		return goInventory{}, fmt.Errorf("no Go packages matched %q in %q", patterns, dir)
	}

	// Load can succeed even when individual packages or their dependencies fail.
	// Never turn an incomplete load into apparent missing exported symbols.
	var problems []string
	for pkg := range packages.Postorder(pkgs) {
		for _, problem := range pkg.Errors {
			problems = append(problems, fmt.Sprintf("%s: %s", pkg.ID, problem))
		}
		for mod := pkg.Module; mod != nil; mod = mod.Replace {
			if mod.Error != nil {
				problems = append(problems, fmt.Sprintf("module %s: %s", mod.Path, mod.Error.Err))
			}
		}
	}
	if len(problems) != 0 {
		slices.Sort(problems)
		return goInventory{}, fmt.Errorf("load Go packages (offline, -mod=readonly):\n%s", strings.Join(slices.Compact(problems), "\n"))
	}
	var module string
	for _, pkg := range pkgs {
		if pkg.Module == nil {
			return goInventory{}, fmt.Errorf("selected Go package %q has no module metadata", pkg.PkgPath)
		}
		if pkg.Module.Main && pkg.Module.Path != "" {
			module = pkg.Module.Path
			break
		}
	}
	if module == "" {
		return goInventory{}, errors.New("index Go: no selected packages belong to the main module")
	}

	inventory := goInventory{
		Module:     module,
		GOOS:       buildEnv.GOOS,
		GOARCH:     buildEnv.GOARCH,
		GoVersion:  buildEnv.GoVersion,
		CGOEnabled: buildEnv.CGOEnabled,
		BuildTags:  buildTags,
		Packages:   make([]string, 0, len(pkgs)),
		Symbols:    make(map[string]goSymbol),
		packages:   make(map[string]*types.Package),
	}
	for pkg := range packages.Postorder(pkgs) {
		if pkg.Types != nil {
			inventory.packages[pkg.PkgPath] = pkg.Types
		}
	}
	qualify := func(pkg *types.Package) string {
		return goIndexPackageName(pkg.Path(), module)
	}
	packageNames := make(map[string]string)
	var checkedModuleDir string
	for _, pkg := range pkgs {
		mod := pkg.Module
		if mod == nil {
			return goInventory{}, fmt.Errorf("selected Go package %q has no module metadata; expected module %q", pkg.PkgPath, module)
		}
		if mod.Path != module {
			return goInventory{}, fmt.Errorf("selected Go package %q belongs to module %q, not %q", pkg.PkgPath, mod.Path, module)
		}
		if !mod.Main || mod.Replace != nil {
			return goInventory{}, fmt.Errorf("selected Go package %q must belong to main module %q, not a dependency or replacement", pkg.PkgPath, module)
		}
		if pkg.PkgPath != module && !strings.HasPrefix(pkg.PkgPath, module+"/") {
			return goInventory{}, fmt.Errorf("selected Go package path %q is outside module %q", pkg.PkgPath, module)
		}
		// A go.work file can make another checkout a main module. Its source
		// must not be attributed to this root's Git commit and working tree.
		if checkedModuleDir == "" || mod.Dir != checkedModuleDir {
			info, err := os.Stat(mod.Dir)
			if err != nil {
				return goInventory{}, fmt.Errorf("inspect Go module directory %q: %w", mod.Dir, err)
			}
			if !os.SameFile(rootInfo, info) {
				return goInventory{}, fmt.Errorf("go module %q was loaded from %q, not requested root %q", module, mod.Dir, dir)
			}
			checkedModuleDir = mod.Dir
		}
		if pkg.Name == "main" || slices.Contains(strings.Split(pkg.PkgPath, "/"), "internal") {
			continue
		}
		if pkg.IllTyped || pkg.Types == nil || !pkg.Types.Complete() {
			return goInventory{}, fmt.Errorf("go package %q has incomplete or invalid type information", pkg.PkgPath)
		}
		packageName := goIndexPackageName(pkg.PkgPath, module)
		if previous, ok := packageNames[packageName]; ok {
			if previous != pkg.PkgPath {
				return goInventory{}, fmt.Errorf("go packages %q and %q have the same inventory name %q", previous, pkg.PkgPath, packageName)
			}
			continue
		}
		packageNames[packageName] = pkg.PkgPath
		inventory.Packages = append(inventory.Packages, packageName)
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			var kind string
			switch obj.(type) {
			case *types.TypeName:
				kind = "type"
			case *types.Func:
				kind = "function"
			case *types.Const:
				kind = "constant"
			case *types.Var:
				kind = "variable"
			default:
				continue
			}
			key := packageName + "." + name
			signature := types.ObjectString(obj, qualify)
			if named, ok := types.Unalias(obj.Type()).(*types.Named); kind == "type" && ok {
				// Fields and methods have their own rows. Avoid embedding private
				// representation details in an exported type's signature.
				switch named.Underlying().(type) {
				case *types.Struct:
					signature = "type " + types.TypeString(obj.Type(), qualify) + " struct"
				case *types.Interface:
					signature = "type " + types.TypeString(obj.Type(), qualify) + " interface"
				}
			}
			inventory.Symbols[key] = goSymbol{Kind: kind, Signature: signature}
			if kind == "type" {
				goIndexMembers(inventory.Symbols, key, obj.Type(), qualify)
			}
		}
	}
	if len(inventory.Packages) == 0 {
		return goInventory{}, errors.New("no Go packages remain after excluding main and internal packages")
	}
	slices.Sort(inventory.Packages)
	inventory.Commit, inventory.Dirty, err = goIndexGit(dir, env)
	if err != nil {
		return goInventory{}, err
	}
	return inventory, nil
}

func goIndexPackageName(importPath, module string) string {
	if importPath == module {
		return path.Base(module)
	}
	return strings.TrimPrefix(importPath, module+"/")
}

func goIndexMembers(symbols map[string]goSymbol, key string, typ types.Type, qualify types.Qualifier) {
	// Method sets resolve promotion and ambiguity, including interfaces and
	// aliases. Prefer the value selection when both T and *T expose a method.
	for _, receiver := range []types.Type{typ, types.NewPointer(typ)} {
		for method := range types.NewMethodSet(receiver).Methods() {
			if !method.Obj().Exported() {
				continue
			}
			name := key + "." + method.Obj().Name()
			if _, exists := symbols[name]; !exists {
				symbols[name] = goSymbol{Kind: "method", Signature: types.SelectionString(method, qualify)}
			}
		}
	}

	// Traversal discovers candidate field names only; lookup on the original
	// type decides whether a field is actually selectable, rather than shadowed
	// or ambiguous. Visiting each struct once also handles recursive embedding.
	seen := make(map[*types.Struct]bool)
	var visit func(types.Type)
	visit = func(candidate types.Type) {
		underlying := candidate.Underlying()
		if pointer, ok := underlying.(*types.Pointer); ok {
			// Only one implicit indirection is allowed. In particular, do not
			// chase a defined pointer cycle such as type P *P.
			underlying = pointer.Elem().Underlying()
		}
		structure, ok := underlying.(*types.Struct)
		if !ok || seen[structure] {
			return
		}
		seen[structure] = true
		for field := range structure.Fields() {
			if field.Exported() {
				selected, _, _ := types.LookupFieldOrMethod(typ, true, nil, field.Name())
				if selected, ok := selected.(*types.Var); ok && selected.IsField() {
					symbols[key+"."+field.Name()] = goSymbol{Kind: "field", Signature: types.ObjectString(selected, qualify)}
				}
			}
			// Unexported embedded types can promote exported fields, but their
			// private field names must not become inventory keys.
			if field.Embedded() {
				visit(field.Type())
			}
		}
	}
	visit(typ)
}

// Settings without '=' remove a variable instead of setting an empty value.
func goIndexEnv(env []string, overrides ...string) []string {
	env = slices.Clone(env)
	for _, setting := range overrides {
		key, _, set := strings.Cut(setting, "=")
		env = slices.DeleteFunc(env, func(entry string) bool {
			name, _, _ := strings.Cut(entry, "=")
			return name == key || (runtime.GOOS == "windows" && strings.EqualFold(name, key))
		})
		if set {
			env = append(env, setting)
		}
	}
	return env
}

// goIndexBuildTags inspects GOFLAGS without rewriting it. Like the go command,
// it accepts whole words enclosed in single or double quotes, with no escaping.
func goIndexBuildTags(flags, tags string) (string, error) {
	var inheritedTags, moduleMode, toolExec string
	for flags = strings.TrimLeft(flags, " \t\r\n"); flags != ""; flags = strings.TrimLeft(flags, " \t\r\n") {
		var word string
		if flags[0] == '\'' || flags[0] == '"' {
			end := strings.IndexByte(flags[1:], flags[0])
			if end < 0 {
				return "", errors.New("invalid GOFLAGS: unterminated quoted word")
			}
			word, flags = flags[1:end+1], flags[end+2:]
		} else if end := strings.IndexAny(flags, " \t\r\n"); end >= 0 {
			word, flags = flags[:end], flags[end:]
		} else {
			word, flags = flags, ""
		}
		name, value, _ := strings.Cut(word, "=")
		switch name {
		case "-tags", "--tags":
			inheritedTags = value
		case "-mod", "--mod":
			moduleMode = value
		case "-toolexec", "--toolexec":
			toolExec = value
		}
	}
	if moduleMode != "" && moduleMode != "readonly" {
		return "", fmt.Errorf("GOFLAGS -mod=%s conflicts with read-only Go indexing; remove it or use -mod=readonly", moduleMode)
	}
	if toolExec != "" {
		return "", errors.New("GOFLAGS -toolexec is not supported: Go indexing must not execute custom tool wrappers")
	}
	if tags != "" {
		return tags, nil
	}
	return inheritedTags, nil
}

func goIndexOutput(root string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && len(exitError.Stderr) != 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return output, nil
}

func goIndexGit(root string, env []string) (string, *bool, error) {
	// Porcelain v2 reports both HEAD and dirty state, including unborn branches.
	// Avoid index refresh writes and configured filesystem-monitor hooks. Do
	// not let an enclosing Git command redirect this query to another tree.
	env = goIndexEnv(env, "LC_ALL=C", "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_NAMESPACE")
	data, err := goIndexOutput(root, env, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal", "--ignore-submodules=none", "--no-renames")
	if err != nil {
		var exitError *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) || (errors.As(err, &exitError) && exitError.ExitCode() == 128 && strings.Contains(string(exitError.Stderr), "not a git repository")) {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("read Go inventory Git metadata: %w", err)
	}
	var commit string
	var foundHead, dirty bool
	for record := range strings.SplitSeq(string(data), "\x00") {
		if oid, ok := strings.CutPrefix(record, "# branch.oid "); ok {
			commit, foundHead = oid, true
		} else if record != "" && !strings.HasPrefix(record, "# ") {
			dirty = true
		}
	}
	if !foundHead {
		return "", nil, errors.New("git status did not report HEAD for Go inventory metadata")
	}
	if commit == "(initial)" {
		commit = ""
	}
	return commit, &dirty, nil
}
