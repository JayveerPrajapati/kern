package domain

import (
	"strings"
	"testing"
)

func TestContextPacketValidateVersionZero(t *testing.T) {
	var pkt ContextPacket // zero EnvelopeVersion = pre-versioning packet
	if err := pkt.Validate(); err != nil {
		t.Fatalf("Validate() with zero EnvelopeVersion: unexpected error: %v", err)
	}
}

func TestContextPacketValidateVersionV1(t *testing.T) {
	pkt := ContextPacket{EnvelopeVersion: EnvelopeVersionV1}
	if err := pkt.Validate(); err != nil {
		t.Fatalf("Validate() with EnvelopeVersionV1: unexpected error: %v", err)
	}
}

func TestContextPacketValidateVersionV2Rejected(t *testing.T) {
	pkt := ContextPacket{EnvelopeVersion: EnvelopeVersionV1 + 1}
	err := pkt.Validate()
	if err == nil {
		t.Fatal("Validate() with version 2: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported envelope version 2") {
		t.Fatalf("Validate() error = %q, want mention of unsupported version 2", err.Error())
	}
	if !strings.Contains(err.Error(), "(latest: 1)") {
		t.Fatalf("Validate() error = %q, want latest version in message", err.Error())
	}
}

func TestContextPacketMigrateV1NoMutation(t *testing.T) {
	pkt := ContextPacket{EnvelopeVersion: EnvelopeVersionV1, Task: "add caching"}
	if err := pkt.Migrate(); err != nil {
		t.Fatalf("Migrate() on V1: unexpected error: %v", err)
	}
	if pkt.EnvelopeVersion != EnvelopeVersionV1 {
		t.Fatalf("Migrate() mutated envelope version: got %d, want %d", pkt.EnvelopeVersion, EnvelopeVersionV1)
	}
	if pkt.Task != "add caching" {
		t.Fatalf("Migrate() mutated packet contents: task = %q, want %q", pkt.Task, "add caching")
	}
}
