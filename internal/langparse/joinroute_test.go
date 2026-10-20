package langparse

import (
	"testing"
)

func TestJoinRoute(t *testing.T) {
	cases := []struct{ base, route, want string }{
		{"", "/users", "/users"},
		{"/api", "", "/api"},
		{"/api", "/users", "/api/users"},
		{"api/", "/users/", "/api/users"},
		{"", "", ""},
		{"", "/", "/"},
		{"/", "", "/"},
		{"/api", "/", "/api"},
	}
	for _, c := range cases {
		if got := joinRoute(c.base, c.route); got != c.want {
			t.Errorf("joinRoute(%q,%q) = %q, want %q", c.base, c.route, got, c.want)
		}
	}
}

func TestCleanHandler(t *testing.T) {
	cases := []struct{ in, want string }{
		{"users#index", "index"},
		{"UserController@store", "store"},
		{"[UserController::class, 'show']", "show"},
		{"views.detail", "detail"},
		{"[UserController::class, ProfileController::class]", "UserController"},
	}
	for _, c := range cases {
		if got := cleanHandler(c.in); got != c.want {
			t.Errorf("cleanHandler(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
