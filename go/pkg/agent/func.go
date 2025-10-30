// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// FuncParameter represents a parameter for a tool function.
type FuncParameter struct {
	Name        string
	Description string
	Type        string
}

var _ Tool = (*Func)(nil)

// Func represents a tool that wraps a Go function to make it callable by AI models.
type Func struct {
	Name                 string
	Description          string
	Parameters           []FuncParameter
	AdditionalProperties map[string]any

	// Func is the function to be called when the tool is invoked.
	// The function can have any signature, but must return either nothing, a single value,
	// or a value and an error. If the first parameter of Func is context.Context, it will be
	// passed the context when the tool is called.
	Func any

	initOnce sync.Once
	initErr  error

	wantContext bool
	hasError    bool
}

func (t *Func) ToolInfo() (name string, description string) {
	return t.Name, t.Description
}

func (t *Func) Properties() map[string]any {
	return t.AdditionalProperties
}

// NewFunc creates a new [Func] with the given name, description, parameters, and function.
// See [Func] for more details.
func NewFunc(name string, description string, params []FuncParameter, fn any) (*Func, error) {
	t := &Func{
		Name:        name,
		Description: description,
		Parameters:  params,
		Func:        fn,
	}
	if err := t.init(); err != nil {
		return nil, err
	}
	return t, nil
}

// MustNewFunc creates a new Func and panics if there is an error.
// See [NewFunc] for more details.
func MustNewFunc(name string, description string, params []FuncParameter, fn any) *Func {
	t, err := NewFunc(name, description, params, fn)
	if err != nil {
		panic(err)
	}
	return t
}

var (
	typeOfContext = reflect.TypeOf((*context.Context)(nil)).Elem()
	typeOfError   = reflect.TypeOf((*error)(nil)).Elem()
)

func (t *Func) init() error {
	t.initOnce.Do(func() {
		if t.Func == nil {
			t.initErr = fmt.Errorf("tool %q: Fn cannot be nil", t.Name)
			return
		}
		fnType := reflect.TypeOf(t.Func)
		if fnType.Kind() != reflect.Func {
			t.initErr = fmt.Errorf("tool %q: Fn must be a function", t.Name)
			return
		}
		if t.Name == "" {
			t.Name = runtime.FuncForPC(reflect.ValueOf(t.Func).Pointer()).Name()
			if nameParts := strings.Split(t.Name, "."); len(nameParts) != 0 {
				t.Name = nameParts[len(nameParts)-1]
			}
		}

		switch fnType.NumOut() {
		case 0:
			// no return values
		case 1:
			// one return value, check if it's error
			t.hasError = fnType.Out(0).Implements(typeOfError)
		case 2:
			// two return values, second must be error
			if !fnType.Out(1).Implements(typeOfError) {
				t.initErr = fmt.Errorf("tool %q: second return value must be of type error, got %q", t.Name, fnType.Out(1).String())
				return
			}
			t.hasError = true
		default:
			t.initErr = fmt.Errorf("tool %q: must have at most two return values, got %d", t.Name, fnType.NumOut())
			return
		}

		nParams := fnType.NumIn()
		wantParams := nParams
		if nParams > 0 && fnType.In(0) == typeOfContext {
			t.wantContext = true
			wantParams--
		}
		if t.Parameters != nil && len(t.Parameters) != wantParams {
			t.initErr = fmt.Errorf("tool %q: parameter count does not match provided Parameters, got %d", t.Name, nParams)
			return
		}
		if t.Parameters == nil {
			t.Parameters = make([]FuncParameter, 0, wantParams)
		}
		for i := range nParams {
			typ := fnType.In(i)
			if i == 0 && t.wantContext {
				continue
			}
			if t.wantContext {
				i--
			}
			name := "arg" + strconv.Itoa(i)
			if i >= len(t.Parameters) {
				t.Parameters = append(t.Parameters, FuncParameter{
					Name: name,
					Type: typ.String(),
				})
			} else {
				p := &t.Parameters[i]
				p.Name = cmp.Or(p.Name, name)
				p.Type = cmp.Or(p.Type, typ.String())
			}
		}
	})
	return t.initErr
}

// Call invokes the tool with the given context and arguments.
func (t *Func) Call(ctx context.Context, args map[string]any) (any, error) {
	if err := t.init(); err != nil {
		return nil, err
	}
	fnValue := reflect.ValueOf(t.Func)
	in := make([]reflect.Value, 0, len(args)+1)
	if t.wantContext {
		in = append(in, reflect.ValueOf(ctx))
	}
	for _, param := range t.Parameters {
		arg, ok := args[param.Name]
		if !ok {
			return nil, fmt.Errorf("missing argument: %s", param.Name)
		}
		in = append(in, reflect.ValueOf(arg))
	}
	out := fnValue.Call(in)
	switch len(out) {
	case 0:
		return nil, nil
	case 1:
		if t.hasError {
			if !out[0].IsNil() {
				return nil, out[0].Interface().(error)
			}
			return nil, nil
		}
		return out[0].Interface(), nil
	case 2:
		var err error
		if t.hasError {
			if !out[1].IsNil() {
				err = out[1].Interface().(error)
			}
		}
		return out[0].Interface(), err
	default:
		panic("unexpected number of return values")
	}
}
