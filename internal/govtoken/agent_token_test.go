package govtoken

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

func signedTestAgent(t *testing.T) *governance.AgentIdentity {
	t.Helper()
	a := governance.NewAgent("signed-1", "Signed One", "default", []governance.Permission{{Resource: "source", Action: "read"}})
	a.TokenSecret = Generate()
	return a
}

func TestAgentTokenRoundTrip(t *testing.T) {
	a := signedTestAgent(t)
	now := time.Now()
	tok, err := Issue(a, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tok, ".") {
		t.Fatalf("token must be timestamp.signature, got %q", tok)
	}
	if err := Verify(a, tok, now.Add(time.Second)); err != nil {
		t.Fatalf("fresh token must verify: %v", err)
	}
}

func TestAgentTokenTamper(t *testing.T) {
	a := signedTestAgent(t)
	tok, err := Issue(a, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Flip one hex character of the signature.
	b := []byte(tok)
	if b[len(b)-1] == '0' {
		b[len(b)-1] = '1'
	} else {
		b[len(b)-1] = '0'
	}
	if err := Verify(a, string(b), time.Now()); err == nil {
		t.Fatal("tampered signature must fail closed")
	}
	// A token minted for a DIFFERENT agent (different secret) must not verify.
	other := governance.NewAgent("other", "Other", "default", nil)
	other.TokenSecret = Generate()
	otherTok, _ := Issue(other, time.Now())
	if err := Verify(a, otherTok, time.Now()); err == nil {
		t.Fatal("token from another secret must fail closed")
	}
}

func TestAgentTokenExpiry(t *testing.T) {
	a := signedTestAgent(t)
	old, err := Issue(a, time.Now().Add(-6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(a, old, time.Now()); err == nil {
		t.Fatal("token older than the 5m window must be rejected")
	}
	future, _ := Issue(a, time.Now().Add(6*time.Minute))
	if err := Verify(a, future, time.Now()); err == nil {
		t.Fatal("token from the far future must be rejected (clock skew bound)")
	}
	// Just inside the window still verifies.
	edge, _ := Issue(a, time.Now().Add(-4*time.Minute))
	if err := Verify(a, edge, time.Now()); err != nil {
		t.Fatalf("token inside the window must verify: %v", err)
	}
}

func TestAgentTokenNoSecretFailClosed(t *testing.T) {
	unsigned := governance.NewAgent("unsigned", "Unsigned", "default", nil)
	if _, err := Issue(unsigned, time.Now()); err == nil {
		t.Fatal("issuing for an unsigned identity must fail")
	}
	if err := Verify(unsigned, "1234.deadbeef", time.Now()); err == nil {
		t.Fatal("verifying against an unsigned identity must fail")
	}
	if err := Verify(nil, "1234.deadbeef", time.Now()); err == nil {
		t.Fatal("verifying against nil must fail")
	}
	for _, bad := range []string{"", "nodots", "1234", "abc.def", "1234.", ".deadbeef"} {
		a := signedTestAgent(t)
		if err := Verify(a, bad, time.Now()); err == nil {
			t.Fatalf("malformed token %q must fail closed", bad)
		}
	}
}
