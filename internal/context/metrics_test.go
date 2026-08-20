package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestMeasureBasics(t *testing.T) {
	pkt := domain.ContextPacket{TokenCount: 100}
	m := Measure(pkt, 3*time.Millisecond)

	if m.TokenReduction <= 0 {
		t.Errorf("TokenReduction = %v, want > 0", m.TokenReduction)
	}
	// With raw = 4x packet: reduction = (400-100)/400 = 75%.
	if m.TokenReduction < 74 || m.TokenReduction > 76 {
		t.Errorf("TokenReduction = %v, want ~75", m.TokenReduction)
	}
	if m.Latency != 3*time.Millisecond {
		t.Errorf("Latency = %v, want 3ms", m.Latency)
	}
	if m.Cost <= 0 {
		t.Errorf("Cost = %v, want > 0", m.Cost)
	}
	if m.RetrievalRelevance != 100 {
		t.Errorf("RetrievalRelevance = %v, want 100 when no facts", m.RetrievalRelevance)
	}
}

func TestMeasureZeroTokens(t *testing.T) {
	m := Measure(domain.ContextPacket{TokenCount: 0}, 0)
	if m.TokenReduction != 0 {
		t.Errorf("TokenReduction = %v, want 0 for empty packet", m.TokenReduction)
	}
}

func TestMeasureRetrievalRelevance(t *testing.T) {
	pkt := domain.ContextPacket{
		TokenCount: 100,
		Facts: []domain.Claim{
			{Statement: "a", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
			{Statement: "b"}, // no evidence
		},
	}
	m := Measure(pkt, time.Nanosecond)
	// 1 of 2 facts has evidence -> 50%.
	if m.RetrievalRelevance != 50 {
		t.Errorf("RetrievalRelevance = %v, want 50", m.RetrievalRelevance)
	}
}

func TestMeasureCostScalesWithTokens(t *testing.T) {
	a := Measure(domain.ContextPacket{TokenCount: 10}, 0)
	b := Measure(domain.ContextPacket{TokenCount: 20}, 0)
	if !(b.Cost > a.Cost) {
		t.Errorf("Cost should scale with tokens: %v -> %v", a.Cost, b.Cost)
	}
}
