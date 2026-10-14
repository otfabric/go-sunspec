// SPDX-License-Identifier: MIT

package devicefile

import (
	"errors"
	"fmt"
)

var (
	// ErrSyntax indicates text that is not a device file: malformed JSON, a
	// member of the wrong type, an unknown member, a point given twice, or
	// data after the document. It is reported through *SyntaxError.
	ErrSyntax = errors.New("devicefile: syntax error")

	// ErrInvalid indicates a device file that parsed but does not describe a
	// device: a model without an ID, a model with both registers and points,
	// point values for a model without a schema, a value of a kind no point
	// takes, or unit ID 0. Values that do not fit their point are reported
	// by the encoder instead, with sunspec.ErrEncode.
	ErrInvalid = errors.New("devicefile: invalid device file")
)

// SyntaxError describes why text could not be parsed as a device file.
// errors.Is matches it against ErrSyntax and against its cause.
type SyntaxError struct {
	// Line and Column locate the problem, both counting from 1. They are
	// zero when the position is not known: for an unknown member, a point
	// given twice, a truncated document and data after the document.
	Line, Column int
	// Err is the cause: an error of encoding/json or a description of the
	// problem.
	Err error
}

// Error formats the failure as "line <l>, column <c>: <cause>", or as the
// cause alone when the position is not known. It does not start with the
// package name: the message is meant to follow a file name.
func (e *SyntaxError) Error() string {
	if e == nil {
		return ErrSyntax.Error()
	}
	msg := "syntax error"
	if e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, msg)
	}
	return msg
}

// Unwrap returns the cause, which may be nil. A nil *SyntaxError unwraps to
// nil.
func (e *SyntaxError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is reports whether target is ErrSyntax, so that errors.Is(err, ErrSyntax)
// holds for every SyntaxError whatever its cause.
func (e *SyntaxError) Is(target error) bool {
	return target == ErrSyntax
}

// ModelError describes what is wrong with one model of a device file.
// errors.Is matches it against ErrInvalid and against its cause.
type ModelError struct {
	// Index is the position of the model in File.Models, counting from 0.
	Index int
	// ModelID is the model's ID, or nil when the problem is that the model
	// has none.
	ModelID *uint16
	// Err describes the problem, naming the group instance and the point it
	// concerns.
	Err error
}

// Error formats the failure as "models[<index>] (model <id>): <problem>",
// without the ID when the model has none.
func (e *ModelError) Error() string {
	if e == nil {
		return ErrInvalid.Error()
	}
	msg := "invalid model"
	if e.Err != nil {
		msg = e.Err.Error()
	}
	if e.ModelID == nil {
		return fmt.Sprintf("models[%d]: %s", e.Index, msg)
	}
	return fmt.Sprintf("models[%d] (model %d): %s", e.Index, *e.ModelID, msg)
}

// Unwrap returns the cause, which may be nil. A nil *ModelError unwraps to
// nil.
func (e *ModelError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is reports whether target is ErrInvalid, so that errors.Is(err, ErrInvalid)
// holds for every ModelError.
func (e *ModelError) Is(target error) bool {
	return target == ErrInvalid
}

// invalidError is a problem of the file as a whole. It matches ErrInvalid.
type invalidError string

func (e invalidError) Error() string { return string(e) }

func (e invalidError) Is(target error) bool { return target == ErrInvalid }
