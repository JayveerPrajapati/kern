//go:build treesitter

package index

import (
	"testing"
)

// hasBase reports whether inherits[subtype] contains an edge of the given
// kind (extends|implements|embeds) targeting base.
func hasBase(inherits map[string][]string, subtype, kind, base string) bool {
	target := kind + ":" + base
	for _, e := range inherits[subtype] {
		if e == target {
			return true
		}
	}
	return false
}

func TestTreeSitterInheritancePython(t *testing.T) {
	src := `class Animal:
    pass

class Cat(Animal, Pet):
    pass
`
	_, _, inherits, _, _ := extractForeign("animals.py", []byte(src), "python")
	if !hasBase(inherits, "Cat", "extends", "Animal") {
		t.Errorf("expected Cat extends Animal, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "extends", "Pet") {
		t.Errorf("expected Cat extends Pet, got %v", inherits["Cat"])
	}
}

func TestTreeSitterInheritanceJava(t *testing.T) {
	src := `interface Pet {
    void cuddle();
}

interface Wild {}

class Cat extends Animal implements Pet, Wild {
}
`
	_, _, inherits, _, _ := extractForeign("Cat.java", []byte(src), "java")
	if !hasBase(inherits, "Cat", "extends", "Animal") {
		t.Errorf("expected Cat extends Animal, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "implements", "Pet") {
		t.Errorf("expected Cat implements Pet, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "implements", "Wild") {
		t.Errorf("expected Cat implements Wild, got %v", inherits["Cat"])
	}
}

func TestTreeSitterInheritanceTS(t *testing.T) {
	src := `interface Speak {}

interface Meow extends Speak {}

class Cat extends Animal implements Pet, Wild {}
`
	_, _, inherits, _, _ := extractForeign("cat.ts", []byte(src), "typescript")
	if !hasBase(inherits, "Cat", "extends", "Animal") {
		t.Errorf("expected Cat extends Animal, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "implements", "Pet") {
		t.Errorf("expected Cat implements Pet, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Meow", "extends", "Speak") {
		t.Errorf("expected Meow extends Speak, got %v", inherits["Meow"])
	}
}

func TestTreeSitterInheritanceRust(t *testing.T) {
	src := `trait Animal {}

struct Cat;

impl Animal for Cat {}

trait Meow: Speak {}
`
	_, _, inherits, _, _ := extractForeign("cat.rs", []byte(src), "rust")
	if !hasBase(inherits, "Cat", "implements", "Animal") {
		t.Errorf("expected Cat implements Animal, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Meow", "extends", "Speak") {
		t.Errorf("expected Meow extends Speak, got %v", inherits["Meow"])
	}
}

func TestTreeSitterInheritanceCpp(t *testing.T) {
	src := `class Cat : public Animal, virtual Pet {};
`
	_, _, inherits, _, _ := extractForeign("cat.cpp", []byte(src), "cpp")
	if !hasBase(inherits, "Cat", "extends", "Animal") {
		t.Errorf("expected Cat extends Animal, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "extends", "Pet") {
		t.Errorf("expected Cat extends Pet, got %v", inherits["Cat"])
	}
}

func TestTreeSitterInheritanceRuby(t *testing.T) {
	src := `class Cat < Animal
end
`
	_, _, inherits, _, _ := extractForeign("cat.rb", []byte(src), "ruby")
	if !hasBase(inherits, "Cat", "extends", "Animal") {
		t.Errorf("expected Cat extends Animal, got %v", inherits["Cat"])
	}
}

func TestTreeSitterInheritancePHP(t *testing.T) {
	src := `<?php class Cat extends Animal implements Pet, Wild {}`
	_, _, inherits, _, _ := extractForeign("cat.php", []byte(src), "php")
	if !hasBase(inherits, "Cat", "extends", "Animal") {
		t.Errorf("expected Cat extends Animal, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "implements", "Pet") {
		t.Errorf("expected Cat implements Pet, got %v", inherits["Cat"])
	}
	if !hasBase(inherits, "Cat", "implements", "Wild") {
		t.Errorf("expected Cat implements Wild, got %v", inherits["Cat"])
	}
}
