package main

import "testing"

// TestIsLoopbackAddr (P2-10): loopback binds stay auth-free; a bare ":port"
// or an explicit non-loopback host binds all interfaces and must be treated
// as non-loopback (fail closed). Parse failures fail closed too.
func TestIsLoopbackAddr(t *testing.T) {
	loopback := []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090"}
	nonLoopback := []string{":8090", "0.0.0.0:8090", "192.168.1.5:8090", "10.0.0.1:443", "garbage"}
	for _, a := range loopback {
		if !isLoopbackAddr(a) {
			t.Errorf("isLoopbackAddr(%q) = false, want true", a)
		}
	}
	for _, a := range nonLoopback {
		if isLoopbackAddr(a) {
			t.Errorf("isLoopbackAddr(%q) = true, want false", a)
		}
	}
}
