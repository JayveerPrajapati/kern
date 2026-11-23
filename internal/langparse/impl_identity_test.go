package langparse

import "testing"

func TestImplIdentity(t *testing.T) {
	cases := []struct {
		header string
		self   string
		disc   string
	}{
		{"impl From<String> for Failure {", "Failure", "impl From<String> for Failure"},
		{"impl From<u8> for Failure", "Failure", "impl From<u8> for Failure"},
		{"impl<T: Clone> Wrapper<T> {", "Wrapper", "impl<T: Clone> Wrapper<T>"},
		{"impl<'a> Trait for &'a Foo {", "Foo", "impl<'a> Trait for &'a Foo"},
		{"impl<T> Foo<T>\n    where T: Bar\n{", "Foo", "impl<T> Foo<T> where T: Bar"},
		{"impl crate::model::Bar {", "Bar", "impl crate::model::Bar"},
		{"not an impl", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		self, disc := ImplIdentity(tc.header)
		if self != tc.self || disc != tc.disc {
			t.Errorf("ImplIdentity(%q) = (%q, %q); want (%q, %q)", tc.header, self, disc, tc.self, tc.disc)
		}
	}
}
