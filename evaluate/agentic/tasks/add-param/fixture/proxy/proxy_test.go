package proxy

import "testing"

func TestUpstreamPort(t *testing.T) {
	if p, err := UpstreamPort("9000"); err != nil || p != 9000 {
		t.Fatalf("UpstreamPort(9000) = %d, %v", p, err)
	}
	if _, err := UpstreamPort("65536"); err == nil {
		t.Fatal("UpstreamPort(65536) should fail")
	}
}
