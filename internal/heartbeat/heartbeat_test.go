package heartbeat

import (
	"testing"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// Two distinct keys, so a verifier and an operator are genuinely different parties.
const (
	verifierKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	operatorKey = "0x8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba"
)

func mustID(t *testing.T, key string) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	id, err := agentid.Format(8453, eip712.AddressToHex(addr))
	if err != nil {
		t.Fatalf("agent id: %v", err)
	}
	return id
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	a := Attestation{
		NodeID:     mustID(t, operatorKey),
		Epoch:      7,
		Slot:       42,
		VerifierID: mustID(t, verifierKey),
	}
	if err := a.Sign(verifierKey, 8453); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := a.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSign_RejectsKeyNotMatchingVerifier(t *testing.T) {
	a := Attestation{
		NodeID:     mustID(t, operatorKey),
		Epoch:      7,
		Slot:       1,
		VerifierID: mustID(t, operatorKey), // declares the OPERATOR as verifier
	}
	// Signing with the verifier key would produce an unverifiable attestation.
	if err := a.Sign(verifierKey, 8453); err == nil {
		t.Error("signing with a key that does not match the declared verifier must be refused")
	}
}

// TestQualified_RefusesSelfAttestation is the F1 core: an operator cannot attest its own
// node, even under a verifier id, because the check is on the recovered signer.
func TestQualified_RefusesSelfAttestation(t *testing.T) {
	opID := mustID(t, operatorKey)
	opAddr, err := OperatorAddress(opID)
	if err != nil {
		t.Fatalf("OperatorAddress: %v", err)
	}

	// A full epoch of heartbeats, all signed by the OPERATOR's own key: self-attestation.
	s := DefaultSpec()
	atts := make([]Attestation, 0, s.SlotsPerEpoch)
	for slot := 0; slot < s.SlotsPerEpoch; slot++ {
		a := Attestation{NodeID: opID, Epoch: 7, Slot: uint32(slot), VerifierID: opID}
		if err := a.Sign(operatorKey, 8453); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		atts = append(atts, a)
	}

	if _, err := Qualified(opAddr, atts, s); err == nil {
		t.Fatal("a node attesting itself must be refused, not counted")
	}
}

// TestQualified_PassesWithIndependentVerifier: a genuinely separate verifier signing a
// full epoch qualifies the node; the A != B rule does not block honest operation.
func TestQualified_PassesWithIndependentVerifier(t *testing.T) {
	opID := mustID(t, operatorKey)
	verID := mustID(t, verifierKey)
	opAddr, _ := OperatorAddress(opID)

	s := DefaultSpec()
	atts := make([]Attestation, 0, s.SlotsPerEpoch)
	for slot := 0; slot < s.SlotsPerEpoch; slot++ {
		a := Attestation{NodeID: opID, Epoch: 7, Slot: uint32(slot), VerifierID: verID}
		if err := a.Sign(verifierKey, 8453); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		atts = append(atts, a)
	}

	ok, err := Qualified(opAddr, atts, s)
	if err != nil {
		t.Fatalf("Qualified: %v", err)
	}
	if !ok {
		t.Error("a full epoch attested by an independent verifier must qualify")
	}
}

func TestQualified_FailsBelowThreshold(t *testing.T) {
	opID := mustID(t, operatorKey)
	verID := mustID(t, verifierKey)
	opAddr, _ := OperatorAddress(opID)

	s := DefaultSpec()
	// Only 50% coverage: well below the 95% bar.
	atts := make([]Attestation, 0)
	for slot := 0; slot < s.SlotsPerEpoch/2; slot++ {
		a := Attestation{NodeID: opID, Epoch: 7, Slot: uint32(slot), VerifierID: verID}
		_ = a.Sign(verifierKey, 8453)
		atts = append(atts, a)
	}
	ok, err := Qualified(opAddr, atts, s)
	if err != nil {
		t.Fatalf("Qualified: %v", err)
	}
	if ok {
		t.Error("50% coverage must not qualify")
	}
}

func TestQualified_FailsOnLongGap(t *testing.T) {
	opID := mustID(t, operatorKey)
	verID := mustID(t, verifierKey)
	opAddr, _ := OperatorAddress(opID)

	// High total coverage but a long middle gap: the node was offline for hours. Total
	// count alone would pass this, which is why the gap rule exists.
	s := Spec{SlotsPerEpoch: 1000, RequiredOnline: 0.95, MaxGap: 18}
	atts := make([]Attestation, 0)
	for slot := 0; slot < 1000; slot++ {
		if slot >= 400 && slot < 500 { // a 100-slot absence
			continue
		}
		a := Attestation{NodeID: opID, Epoch: 7, Slot: uint32(slot), VerifierID: verID}
		_ = a.Sign(verifierKey, 8453)
		atts = append(atts, a)
	}
	ok, err := Qualified(opAddr, atts, s)
	if err != nil {
		t.Fatalf("Qualified: %v", err)
	}
	if ok {
		t.Error("a 100-slot gap must fail even with 90% total coverage")
	}
}

func TestQualified_RejectsSlotOutOfRange(t *testing.T) {
	opID := mustID(t, operatorKey)
	verID := mustID(t, verifierKey)
	opAddr, _ := OperatorAddress(opID)

	a := Attestation{NodeID: opID, Epoch: 7, Slot: 5000, VerifierID: verID}
	_ = a.Sign(verifierKey, 8453)
	if _, err := Qualified(opAddr, []Attestation{a}, Spec{SlotsPerEpoch: 1008, RequiredOnline: 0.95, MaxGap: 18}); err == nil {
		t.Error("a slot outside the epoch must be an error, not silently counted")
	}
}
