package langparse

import (
	"reflect"
	"testing"
)

func TestForeignUnsupportedImportsStayUnbound(t *testing.T) {
	for _, source := range []string{
		"import type { publicCall as invoke } from './core';\n",
		"import { type publicCall as invoke } from './core';\n",
		"import type Core from './core';\n",
		"import * as ns, { a } from './core';\n",
	} {
		if imports := foreignNamedImports([]byte(source), "typescript"); len(imports) != 0 {
			t.Errorf("unsupported runtime import was bound: %q -> %+v", source, imports)
		}
	}
}

func TestForeignMultilineAndDefaultPlusNamedImports(t *testing.T) {
	for _, fixture := range []struct {
		language, source string
		want             []string
	}{
		{"typescript", "import {\n publicCall as invoke,\n other,\n} from './core';\n", []string{"invoke:publicCall", "other:other"}},
		{"typescript", "import Core, { publicCall as invoke } from './core';\n", []string{"Core:default", "invoke:publicCall"}},
		{"python", "from lib.core import (\n    public as invoke,  # note\n    other,\n)\n", []string{"invoke:public", "other:other"}},
	} {
		var got []string
		for _, edge := range foreignNamedImports([]byte(fixture.source), fixture.language) {
			got = append(got, edge.Alias+":"+edge.Symbol)
		}
		if !reflect.DeepEqual(got, fixture.want) {
			t.Errorf("%s %q: got %v want %v", fixture.language, fixture.source, got, fixture.want)
		}
	}
}
