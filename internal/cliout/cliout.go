// Package cliout holds the shared CLI JSON output plumbing used by the kern
// command surface's --json surfaces: indented marshaling with a per-field
// size-clipping safety guard. Extracted from cmd/kern/helpers.go, which
// carried the canonical copy, and from the six extracted CLI families that
// carried verbatim copies of the same reflect machinery (consolidation
// tranche 7). Sites keep a thin local wrapper so a marshal failure routes
// through their own fatal sentinel:
//
//	func printJSON(v any) {
//		if err := cliout.Print(os.Stdout, v); err != nil {
//			fatal("printJSON: %v", err)
//		}
//	}
package cliout

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// maxJSONFieldLen bounds any single string field emitted by Print. It is
// a safety net for --json payloads whose embedded command output can grow
// unboundedly (a `go test -v` log in a verification result measured 606 KB;
// dogfooding F4): oversized fields are truncated with a marker so the payload
// stays valid JSON. The engine-side capture clipping already bounds the common
// paths; this guard exists so no --json surface can dump multi-MB strings to
// the terminal.
const maxJSONFieldLen = 512 * 1024

// jsonTruncMarker is appended to a string field truncated by the Print
// safety guard, so truncated output is visibly marked rather than silently
// cut.
const jsonTruncMarker = "\n… [kern: field truncated at 512 KiB safety cap — full value kept in .kern/audit logs]"

// Print renders v as indented JSON on w (plus a trailing newline) with
// oversized string fields clipped by ClipStrings before marshaling. An
// error is returned for a marshal failure; callers route it through their
// family's fatal sentinel with the historical "printJSON: %v" phrasing.
func Print(w io.Writer, v any) error {
	b, err := json.MarshalIndent(ClipStrings(v), "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return err
	}
	return nil
}

// ClipStrings returns a deep copy of v in which every string longer than
// maxJSONFieldLen is truncated with jsonTruncMarker. Structure, field order,
// and non-string values (including exact int64/float values) are preserved, so
// the JSON shape is identical to an untruncated marshal except for oversized
// strings. Deterministic, no LLM.
func ClipStrings(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	return clipValue(rv).Interface()
}

// clipValue recursively clips rv per the ClipStrings contract.
func clipValue(rv reflect.Value) reflect.Value {
	if !rv.IsValid() {
		return rv
	}
	// Values with a custom JSON marshaler (time.Time, uuid, ...) are safe:
	// they never exceed maxJSONFieldLen, and recursing into their internal
	// struct fields would copy only CanInterface() fields — zeroing types
	// like time.Time whose fields are all unexported.
	if rv.CanInterface() {
		if _, ok := rv.Interface().(json.Marshaler); ok {
			return rv
		}
	}
	switch rv.Kind() {
	case reflect.String:
		if rv.Len() > maxJSONFieldLen {
			return reflect.ValueOf(rv.String()[:maxJSONFieldLen] + jsonTruncMarker)
		}
		return rv
	case reflect.Pointer:
		if rv.IsNil() {
			return rv
		}
		ev := clipValue(rv.Elem())
		nv := reflect.New(ev.Type())
		nv.Elem().Set(ev)
		return nv
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		return clipValue(rv.Elem())
	case reflect.Struct:
		nv := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.NumField(); i++ {
			if rv.Field(i).CanInterface() {
				nv.Field(i).Set(clipValue(rv.Field(i)))
			}
		}
		return nv
	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		nv := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			nv.Index(i).Set(clipValue(rv.Index(i)))
		}
		return nv
	case reflect.Map:
		if rv.IsNil() {
			return rv
		}
		nv := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			nv.SetMapIndex(iter.Key(), clipValue(iter.Value()))
		}
		return nv
	default:
		return rv
	}
}
