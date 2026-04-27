// Copyright (c) Microsoft. All rights reserved.

package messagetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/message"
)

func MessagesEqual(got, want []*message.Message) error {
	var errs []error
	if len(want) != len(got) {
		errs = append(errs, fmt.Errorf("message count mismatch: expected %d, got %d", len(want), len(got)))
	}
	for i := range want {
		if i >= len(got) {
			break
		}
		if err := MessageEqual(got[i], want[i]); err != nil {
			errs = append(errs, fmt.Errorf("message %d mismatch: %v", i, err))
		}
	}
	return errors.Join(errs...)
}

func MessageEqual(got, want *message.Message) error {
	var errs []error
	if want.ID != got.ID {
		errs = append(errs, fmt.Errorf("ID mismatch: expected %s, got %s", want.ID, got.ID))
	}
	if want.Role != got.Role {
		errs = append(errs, fmt.Errorf("role mismatch: expected %s, got %s", want.Role, got.Role))
	}
	if want.CreatedAt != got.CreatedAt {
		errs = append(errs, fmt.Errorf("created at mismatch: expected %v, got %v", want.CreatedAt, got.CreatedAt))
	}
	if want.String() != got.String() {
		errs = append(errs, fmt.Errorf("string representation mismatch:\nexpected: %q\ngot:      %q", want.String(), got.String()))
	}
	if len(want.Contents) != len(got.Contents) {
		errs = append(errs, fmt.Errorf("content count mismatch: expected %d, got %d", len(want.Contents), len(got.Contents)))
	}
	errs = append(errs, ContentsEqual(got.Contents, want.Contents))
	return errors.Join(errs...)
}

func ResponseUpdatesEqual(got, want []*message.ResponseUpdate) error {
	var errs []error
	if len(want) != len(got) {
		errs = append(errs, fmt.Errorf("response update count mismatch: expected %d, got %d", len(want), len(got)))
	}
	for i := range want {
		if i >= len(got) {
			break
		}
		if err := ResponseUpdateEqual(got[i], want[i]); err != nil {
			errs = append(errs, fmt.Errorf("response update %d mismatch: %v", i, err))
		}
	}
	return errors.Join(errs...)
}

func ResponseUpdateEqual(got, want *message.ResponseUpdate) error {
	var errs []error
	if want.MessageID != got.MessageID {
		errs = append(errs, fmt.Errorf("message ID mismatch: expected %s, got %s", want.MessageID, got.MessageID))
	}
	if want.ResponseID != got.ResponseID {
		errs = append(errs, fmt.Errorf("response ID mismatch: expected %s, got %s", want.ResponseID, got.ResponseID))
	}
	if want.Role != got.Role {
		errs = append(errs, fmt.Errorf("role mismatch: expected %s, got %s", want.Role, got.Role))
	}
	if want.CreatedAt != got.CreatedAt {
		errs = append(errs, fmt.Errorf("created at mismatch: expected %v, got %v", want.CreatedAt, got.CreatedAt))
	}
	if want.String() != got.String() {
		errs = append(errs, fmt.Errorf("string representation mismatch:\nexpected: %q\ngot:      %q", want.String(), got.String()))
	}
	errs = append(errs, ContentsEqual(got.Contents, want.Contents))
	return errors.Join(errs...)
}

func ContentsEqual(got, want []message.Content) error {
	var errs []error
	if len(want) != len(got) {
		errs = append(errs, fmt.Errorf("content count mismatch: expected %d, got %d", len(want), len(got)))
	}
	for i := range want {
		if i >= len(got) {
			break
		}
		if err := ContentEqual(got[i], want[i]); err != nil {
			errs = append(errs, fmt.Errorf("content %d mismatch: %v", i, err))
		}
	}
	return errors.Join(errs...)
}

func ContentEqual(got, want message.Content) error {
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		return fmt.Errorf("type mismatch: expected %T, got %T", want, got)
	}

	// Compare headers, but ignore RawRepresentation as it's set during parsing
	gotHeader := got.Header()
	wantHeader := want.Header()

	// Compare AdditionalProperties
	if !reflect.DeepEqual(wantHeader.AdditionalProperties, gotHeader.AdditionalProperties) {
		return fmt.Errorf("header AdditionalProperties mismatch: expected %v, got %v", wantHeader.AdditionalProperties, gotHeader.AdditionalProperties)
	}

	// Compare Annotations
	if !reflect.DeepEqual(wantHeader.Annotations, gotHeader.Annotations) {
		return fmt.Errorf("header Annotations mismatch: expected %v, got %v", wantHeader.Annotations, gotHeader.Annotations)
	}

	// Note: RawRepresentation is intentionally not compared as it's set during parsing

	switch expContent := want.(type) {
	case fmt.Stringer:
		if got.(fmt.Stringer).String() != expContent.String() {
			return fmt.Errorf("%T mismatch: expected %q, got %q", expContent, expContent.String(), got.(fmt.Stringer).String())
		}
	case *message.FunctionCallContent:
		act := got.(*message.FunctionCallContent)
		if expContent.CallID != act.CallID {
			return fmt.Errorf("CallID mismatch: expected %q, got %q", expContent.CallID, act.CallID)
		}
		if expContent.Name != act.Name {
			return fmt.Errorf("name mismatch: expected %q, got %q", expContent.Name, act.Name)
		}
		if expContent.Arguments != act.Arguments {
			return fmt.Errorf("arguments mismatch: expected %q, got %q", expContent.Arguments, act.Arguments)
		}
		// Compare Error fields
		if (expContent.Error == nil) != (act.Error == nil) {
			return fmt.Errorf("error presence mismatch: expected %v, got %v", expContent.Error, act.Error)
		}
		if expContent.Error != nil && act.Error != nil && expContent.Error.Error() != act.Error.Error() {
			return fmt.Errorf("error message mismatch: expected %q, got %q", expContent.Error, act.Error)
		}
	case *message.FunctionResultContent:
		act := got.(*message.FunctionResultContent)
		if expContent.CallID != act.CallID {
			return fmt.Errorf("CallID mismatch: expected %q, got %q", expContent.CallID, act.CallID)
		}

		// Compare Error fields
		if (expContent.Error == nil) != (act.Error == nil) {
			return fmt.Errorf("error presence mismatch: expected %q, got %q", expContent.Error, act.Error)
		}
		if expContent.Error != nil && act.Error != nil && expContent.Error.Error() != act.Error.Error() {
			return fmt.Errorf("error message mismatch: expected %q, got %q", expContent.Error, act.Error)
		}

		// Compare Result - handle json.RawMessage wrapping
		expResult := expContent.Result
		actResult := act.Result

		// If actual result is json.RawMessage, try to unmarshal it
		if actRaw, ok := actResult.(json.RawMessage); ok {
			var unmarshaled any
			if err := json.Unmarshal(actRaw, &unmarshaled); err == nil {
				actResult = unmarshaled
			}
		}
		if !reflect.DeepEqual(expResult, actResult) {
			return fmt.Errorf("result mismatch:\nexpected: %#v\ngot:      %#v", expResult, actResult)
		}
	default:
		if !reflect.DeepEqual(expContent, got) {
			return fmt.Errorf("content mismatch:\nexpected: %#v\ngot:      %#v", expContent, got)
		}
	}
	return nil
}
