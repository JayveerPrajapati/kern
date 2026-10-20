package admin

import "testing"

func TestPortOnlyPrivileged(t *testing.T) {
	if p, err := Port("443"); err != nil || p != 443 {
		t.Fatalf("Port(443) = %d, %v", p, err)
	}
	if _, err := Port("8080"); err == nil {
		t.Fatal("Port(8080) should fail: admin only accepts privileged ports")
	}
}
