package index

import "testing"

func TestDeclarationID(t *testing.T) {
	original := Symbol{File: "src/service.java", Lang: "java", Kind: "method", Receiver: "Service", Name: "run", Line: 10, Params: []string{"int"}}
	variants := []Symbol{original, original, original, original, original, original, original}
	variants[0].File = "other/service.java"
	variants[1].Lang = "typescript"
	variants[2].Kind = "func"
	variants[3].Receiver = "Other"
	variants[4].Outer = "Container"
	variants[5].Line = 11
	variants[6].Params = []string{"string"}
	for _, variant := range variants {
		if original.DeclarationID() == variant.DeclarationID() {
			t.Errorf("declarations collided: %+v and %+v", original, variant)
		}
	}
	copy := original
	copy.File = "src\\service.java"
	copy.End = 50
	copy.Entry = true
	copy.Confidence = ConfidenceLow
	if original.DeclarationID() != copy.DeclarationID() {
		t.Fatal("path separators or non-identity metadata changed declaration ID")
	}
	copy = original
	original.Params = nil
	copy.Params = []string{}
	if original.DeclarationID() != copy.DeclarationID() {
		t.Fatal("nil and empty parameter lists must have the same identity")
	}
}
