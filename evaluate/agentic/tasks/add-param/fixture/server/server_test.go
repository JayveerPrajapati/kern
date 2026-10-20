package server

import "testing"

func TestListenPort(t *testing.T) {
	if p, err := ListenPort("8080"); err != nil || p != 8080 {
		t.Fatalf("ListenPort(8080) = %d, %v", p, err)
	}
	if _, err := ListenPort("70000"); err == nil {
		t.Fatal("ListenPort(70000) should fail")
	}
}
