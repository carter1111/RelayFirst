package agentid

import (
	"strings"
	"testing"
)

// TestParse_Valid covers the forms that must work, including the normalization
// guarantee: an uppercase address and its lowercase form are one identity, not
// two.
func TestParse_Valid(t *testing.T) {
	cases := []struct {
		in      string
		wantCID uint64
		wantAdr string
	}{
		{
			in:      "agent:eip155:8453:0x7F4dB0D9C4B8A6BCE8E1C22dA9c419E6e1F3A8B5",
			wantCID: 8453,
			wantAdr: "0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5",
		},
		{
			in:      "agent:eip155:1:0x0000000000000000000000000000000000000000",
			wantCID: 1,
			wantAdr: "0x0000000000000000000000000000000000000000",
		},
		{
			// chainId 0 is a valid uint64 and must not be confused with "missing".
			in:      "agent:eip155:0:0x0000000000000000000000000000000000000001",
			wantCID: 0,
			wantAdr: "0x0000000000000000000000000000000000000001",
		},
		{
			// The largest uint64 must parse; rejecting it would make the grammar
			// narrower than the type it claims to hold.
			in:      "agent:eip155:18446744073709551615:0x0000000000000000000000000000000000000002",
			wantCID: 18446744073709551615,
			wantAdr: "0x0000000000000000000000000000000000000002",
		},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) failed: %v", c.in, err)
			continue
		}
		if got.ChainID != c.wantCID {
			t.Errorf("Parse(%q).ChainID = %d, want %d", c.in, got.ChainID, c.wantCID)
		}
		if got.Address != c.wantAdr {
			t.Errorf("Parse(%q).Address = %q, want %q", c.in, got.Address, c.wantAdr)
		}
	}
}

// TestParse_RejectsChainIDVariants is the L1 regression, generalized.
//
// The chain id component was once skipped entirely, so all of these validated.
// A signature does not save them: the chain id is inside the signed string, so it
// is authenticated as that string, never as a chain id.
func TestParse_RejectsChainIDVariants(t *testing.T) {
	const addr = "0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"
	bad := []struct {
		name string
		id   string
	}{
		{"empty chainId", "agent:eip155::" + addr},
		{"non-numeric chainId", "agent:eip155:not-a-number:" + addr},
		{"negative chainId", "agent:eip155:-5:" + addr},
		{"hex chainId", "agent:eip155:0x1f:" + addr},
		{"overflowing chainId", "agent:eip155:99999999999999999999999999:" + addr},
		{"chainId with spaces", "agent:eip155: 8453:" + addr},
		{"chainId with trailing space", "agent:eip155:8453 :" + addr},
		{"float chainId", "agent:eip155:1.0:" + addr},
		{"chainId with underscore", "agent:eip155:1_000:" + addr},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.id); err == nil {
				t.Errorf("Parse(%q) must fail: the chainId is not a decimal uint64", c.id)
			}
		})
	}
}

// TestParse_RejectsAddressVariants covers the other half. The address shape is
// part of what the identifier means, so a malformed one is not a slightly wrong
// identity — it is not an identity.
func TestParse_RejectsAddressVariants(t *testing.T) {
	bad := []struct {
		name string
		id   string
	}{
		{"empty address", "agent:eip155:1:"},
		{"no 0x prefix", "agent:eip155:1:7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"},
		{"too short", "agent:eip155:1:0x7f4d"},
		{"too long", "agent:eip155:1:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b500"},
		{"non-hex char", "agent:eip155:1:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8bz"},
		{"missing address after colon", "agent:eip155:1"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.id); err == nil {
				t.Errorf("Parse(%q) must fail", c.id)
			}
		})
	}
}

func TestParse_RejectsWrongScheme(t *testing.T) {
	bad := []string{
		"",
		"agent:eip155",
		"agent:eip-155:1:0x0000000000000000000000000000000000000001",
		"AGENT:EIP155:1:0x0000000000000000000000000000000000000001",
		"agent:1:0x0000000000000000000000000000000000000001",
		"eip155:1:0x0000000000000000000000000000000000000001",
	}
	for _, in := range bad {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) must fail: the scheme is fixed", in)
		}
	}
}

// TestRoundTrip pins the property that makes string comparison safe: parsing and
// re-rendering is the identity, so two identifiers that differ only in case are
// recognized as the same agent rather than stored as two.
func TestRoundTrip(t *testing.T) {
	cases := []string{
		"agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5",
		"agent:eip155:0:0x0000000000000000000000000000000000000000",
		"agent:eip155:18446744073709551615:0x0000000000000000000000000000000000000001",
	}
	for _, in := range cases {
		parsed, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := parsed.String(); got != in {
			t.Errorf("round trip changed the id:\n  in  %s\n  out %s", in, got)
		}
	}
}

func TestFormat_ValidatesAddress(t *testing.T) {
	got, err := Format(8453, "0x7F4dB0D9C4B8A6BCE8E1C22dA9c419E6e1F3A8B5")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	want := "agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"
	if got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}

	// Format must not be able to build something Parse rejects: an identifier
	// that cannot survive a round trip is a latent bug in whatever stores it.
	if _, err := Format(1, "not-an-address"); err == nil {
		t.Fatal("Format must reject a malformed address")
	}
}

// TestParse_NeverReturnsPartiallyParsed guards against the failure mode where a
// caller ignores the error and uses the zero value, which would look like chain 0
// at the zero address — a real-looking identity.
func TestParse_NeverReturnsPartiallyParsed(t *testing.T) {
	for _, in := range []string{"", "agent:eip155:1:", "agent:eip155::0x00"} {
		got, err := Parse(in)
		if err == nil {
			continue
		}
		if got.ChainID != 0 || got.Address != "" {
			t.Errorf("Parse(%q) returned a partial result %+v alongside its error; "+
				"a caller that ignores the error would use it as a real identity", in, got)
		}
	}
}

// TestNoUppercaseLeaks confirms normalization is total, so a downstream consumer
// comparing addresses as strings cannot be fooled by case.
func TestNoUppercaseLeaks(t *testing.T) {
	parsed, err := Parse("agent:eip155:1:0xABCDEF0123456789ABCDEF0123456789ABCDEF01")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if strings.ToLower(parsed.Address) != parsed.Address {
		t.Errorf("address %q must be normalized to lowercase", parsed.Address)
	}
}
