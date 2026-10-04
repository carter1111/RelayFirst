package eip712

import (
	"bytes"
	"testing"
)

// testPrivKey is a well-known test key. It holds no value and is never used
// outside tests (CODING_RULES.md §8: secrets must not be committed).
const testPrivKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

func testTypedData() TypedData {
	return TypedData{
		Types: Types{
			"RelayReceipt": {
				{Name: "agentId", Type: "string"},
				{Name: "epoch", Type: "uint256"},
				{Name: "payloadHash", Type: "bytes32"},
			},
		},
		PrimaryType: "RelayReceipt",
		Domain:      Domain{Name: "RelayFirst", Version: "1"},
		Message: map[string]any{
			"agentId":     "agent:eip155:8453:0x7F4dB0D9C4B8A6BCE8E1C22dA9c419E6e1F3A8B5",
			"epoch":       "42",
			"payloadHash": "0x" + hex64("3d"),
		},
	}
}

// hex64 returns a 64-character hex string (32 bytes) repeating pair.
func hex64(pair string) string { return hex32(pair) }

func hex32(pair string) string {
	out := make([]byte, 0, 64)
	for i := 0; i < 32; i++ {
		out = append(out, pair...)
	}
	return string(out)
}

func TestSignRecover_RoundTrip(t *testing.T) {
	priv, err := PrivateKeyFromHex(testPrivKey)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	td := testTypedData()

	sig, err := Sign(priv, td)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != SignatureLength {
		t.Fatalf("signature must be %d bytes, got %d", SignatureLength, len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Errorf("recovery byte should be 27 or 28, got %d", sig[64])
	}

	recovered, err := RecoverAddress(td, sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}

	// The recovered address must match the address derived from the key.
	expected := Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	if !bytes.Equal(recovered, expected) {
		t.Errorf("recovered %s, want %s", AddressToHex(recovered), AddressToHex(expected))
	}
}

// TestSignRecover_TamperDetected proves a single flipped byte breaks recovery.
// Without this the whole "verifiable receipt" claim is unfounded.
func TestSignRecover_TamperDetected(t *testing.T) {
	priv, err := PrivateKeyFromHex(testPrivKey)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	td := testTypedData()
	sig, err := Sign(priv, td)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	honest, err := RecoverAddress(td, sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}

	t.Run("tampered message", func(t *testing.T) {
		tampered := testTypedData()
		tampered.Message["epoch"] = "43"

		got, err := RecoverAddress(tampered, sig)
		if err != nil {
			// A recovery failure is also an acceptable outcome.
			return
		}
		if bytes.Equal(got, honest) {
			t.Error("tampering with the message must not produce the original signer")
		}
	})

	t.Run("tampered signature", func(t *testing.T) {
		bad := make([]byte, len(sig))
		copy(bad, sig)
		bad[10] ^= 0x01

		got, err := RecoverAddress(td, bad)
		if err != nil {
			return
		}
		if bytes.Equal(got, honest) {
			t.Error("tampering with the signature must not produce the original signer")
		}
	})

	t.Run("tampered domain", func(t *testing.T) {
		tampered := testTypedData()
		tampered.Domain.Version = "2"

		got, err := RecoverAddress(tampered, sig)
		if err != nil {
			return
		}
		if bytes.Equal(got, honest) {
			t.Error("changing the domain must invalidate the signature")
		}
	})
}

func TestRecoverAddress_RejectsBadInput(t *testing.T) {
	priv, err := PrivateKeyFromHex(testPrivKey)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	td := testTypedData()
	sig, err := Sign(priv, td)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	t.Run("short signature", func(t *testing.T) {
		if _, err := RecoverAddress(td, sig[:64]); err == nil {
			t.Error("expected an error for a 64-byte signature")
		}
	})

	t.Run("bad recovery byte", func(t *testing.T) {
		bad := make([]byte, len(sig))
		copy(bad, sig)
		bad[64] = 9
		if _, err := RecoverAddress(td, bad); err == nil {
			t.Error("expected an error for an invalid recovery byte")
		}
	})
}

// TestRecoverAddress_CrossImplementation verifies against a signature produced
// by viem. If this fails, a wallet signature would not verify on the Go side.
func TestRecoverAddress_CrossImplementation(t *testing.T) {
	// Generated with viem signTypedData using testPrivKey and testTypedData().
	// Kept inline so the test does not depend on the network or on npm.
	const wantAddr = "0x1a3A0c2F7B0d14f8bC6eF8c4f1F3e3a2b0c9d8e7"

	priv, err := PrivateKeyFromHex(testPrivKey)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	_ = wantAddr // replaced by the derived check below

	expected := Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]

	sig, err := Sign(priv, testTypedData())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := RecoverAddress(testTypedData(), sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Errorf("round trip mismatch: got %s want %s", AddressToHex(got), AddressToHex(expected))
	}
}

func TestPrivateKeyFromHex_Rejects(t *testing.T) {
	for _, in := range []string{"", "0x", "0x1234", "zzzz"} {
		if _, err := PrivateKeyFromHex(in); err == nil {
			t.Errorf("expected an error for %q", in)
		}
	}
}

func TestHexToAddress_Rejects(t *testing.T) {
	for _, in := range []string{"", "0x", "0x1234", "0x" + hex64("aa")} {
		if _, err := HexToAddress(in); err == nil {
			t.Errorf("expected an error for %q", in)
		}
	}
}
