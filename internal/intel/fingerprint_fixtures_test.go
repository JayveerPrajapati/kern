// Code generated from the blueprint duplication oracle fixtures
// (blueprintIO/internal/blueprint/checks/duplication/fixtures.go) - DO NOT EDIT.
// These source strings are the canonical fixture inputs for the parity test:
// the expected fingerprints below were produced by blueprint's own
// ComputeFingerprint and encoded as literals so the kern port is verified
// against the exact same inputs and outputs.
package intel

// blueprintFixtureExactShared is the verbatim source of blueprintFixtureExactShared.
const blueprintFixtureExactShared = `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`

// blueprintFixtureUnrelatedVault is the verbatim source of blueprintFixtureUnrelatedVault.
const blueprintFixtureUnrelatedVault = `package vault

// Process encrypts the payload with a fixed XOR key.
func Process(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	key := byte(0x5a)
	for i := range data {
		data[i] ^= key
	}
	return nil
}
`

// blueprintFixtureWrapperAPI is the verbatim source of blueprintFixtureWrapperAPI.
const blueprintFixtureWrapperAPI = `package api

import (
	"log"

	"example.com/repo/shared"
)

func RetryWithLog(req *shared.Request) error {
	log.Println("retrying request")
	return shared.RetryRequest(req)
}
`
