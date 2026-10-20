package langparse

import (
	"reflect"
	"testing"
)

func TestForeignExportsExtraction(t *testing.T) {
	source := "export default class Widget {}\n" +
		"export { a as b, c, type T } from './m';\n" +
		"export {\n  d as e,\n} from './n';\n" +
		"export * from './all';\n" +
		"export * as ns from './skipped';\n" +
		"export { local as default };\n" +
		"export { local as publicName, other };\n" +
		"export const value = 1;\n" +
		"export interface Contract {}\n" +
		"export function fn() { export_default_not_here(); }\n"
	want := []ExportEdge{
		{Kind: "default", Symbol: "Widget"},
		{Kind: "named", Name: "b", Symbol: "a", Path: "./m"},
		{Kind: "named", Name: "c", Symbol: "c", Path: "./m"},
		{Kind: "named", Name: "e", Symbol: "d", Path: "./n"},
		{Kind: "star", Path: "./all"},
		{Kind: "default", Symbol: "local"},
		{Kind: "named", Name: "publicName", Symbol: "local"},
		{Kind: "named", Name: "other", Symbol: "other"},
		{Kind: "named", Name: "value", Symbol: "value"},
		{Kind: "named", Name: "fn", Symbol: "fn"},
	}
	if got := foreignExports([]byte(source), "typescript"); !reflect.DeepEqual(got, want) {
		t.Fatalf("exports = %+v\nwant     %+v", got, want)
	}
	if got := foreignExports([]byte(source), "python"); got != nil {
		t.Fatalf("python must not produce exports: %+v", got)
	}
}
