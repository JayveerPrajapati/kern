package sandbox

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAssessNetworkDetectsSignatures(t *testing.T) {
	p := assessNetwork("fetch: dial tcp 10.0.0.1:443: connect: connection refused\nlookup api.example.com: no such host")
	if p.Isolated {
		t.Fatal("sandbox.Run is not network-isolated; the policy must say so")
	}
	if p.NetnsAvail != (runtime.GOOS == "linux") {
		t.Fatalf("NetnsAvail must follow the platform, got %v", p.NetnsAvail)
	}
	joined := strings.Join(p.Hits, ",")
	for _, want := range []string{"connection refused", "dial tcp", "no such host"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected signature %q among hits %v", want, p.Hits)
		}
	}
}

func TestAssessNetworkCleanOutput(t *testing.T) {
	p := assessNetwork("hello world\nbuild succeeded\n")
	if len(p.Hits) != 0 {
		t.Fatalf("expected no hits, got %v", p.Hits)
	}
	if s := p.Summary(); !strings.Contains(s, "not isolated") || !strings.Contains(s, "no network-error signatures") {
		t.Fatalf("summary must state the posture, got %q", s)
	}
}

func TestAssessNetworkCapsAndDedupes(t *testing.T) {
	p := assessNetwork("connection refused connection reset no route to host dial tcp tls handshake network unreachable name resolution connection timed out dial udp")
	if len(p.Hits) > maxNetworkHits {
		t.Fatalf("hits must cap at %d, got %d (%v)", maxNetworkHits, len(p.Hits), p.Hits)
	}
	seen := map[string]bool{}
	for _, h := range p.Hits {
		if seen[h] {
			t.Fatalf("duplicate hit %q in %v", h, p.Hits)
		}
		seen[h] = true
	}
}

func TestRunRecordsNetworkPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo is a shell builtin on windows")
	}
	root := t.TempDir()
	res := Run(context.TODO(), root, "echo", []string{"dial tcp: connection refused"}, time.Second)
	if res.Network == nil {
		t.Fatal("Run must record a network policy (G-3)")
	}
	if len(res.Network.Hits) == 0 {
		t.Fatalf("expected network signatures from output, got %+v", res.Network)
	}
	if res.Network.Isolated {
		t.Fatal("sandbox.Run must report Isolated=false honestly")
	}
}
