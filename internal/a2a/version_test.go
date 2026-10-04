package a2a

import (
	"errors"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in         string
		wantMajor  int
		wantMinor  int
		wantErr    bool
		errComment string
	}{
		{in: "1.0", wantMajor: 1, wantMinor: 0},
		{in: "0.3", wantMajor: 0, wantMinor: 3},
		{in: "2.10", wantMajor: 2, wantMinor: 10},
		// A patch component must parse and be ignored, per the spec: patch does
		// not affect compatibility, so 1.0.7 and 1.0.2 are the same version.
		{in: "1.0.7", wantMajor: 1, wantMinor: 0, errComment: "patch dropped"},
		{in: "1.0.0", wantMajor: 1, wantMinor: 0, errComment: "patch dropped"},
		// Whitespace is tolerated once, at the edges.
		{in: "  1.0  ", wantMajor: 1, wantMinor: 0},

		{in: "", wantErr: true},
		{in: "1", wantErr: true, errComment: "major only is not a version"},
		{in: "1.", wantErr: true, errComment: "trailing dot"},
		{in: ".0", wantErr: true, errComment: "empty major"},
		{in: "1.0.0.0", wantErr: true, errComment: "too many components"},
		{in: "a.b", wantErr: true},
		{in: "1.x", wantErr: true},
		{in: "1.0.x", wantErr: true, errComment: "malformed patch is still malformed"},
		{in: "-1.0", wantErr: true, errComment: "negative major"},
		{in: "1.-1", wantErr: true, errComment: "negative minor"},
	}

	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseVersion(%q) must fail (%s), got %v", c.in, c.errComment, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseVersion(%q) failed: %v", c.in, err)
			continue
		}
		if got.Major != c.wantMajor || got.Minor != c.wantMinor {
			t.Errorf("ParseVersion(%q) = %d.%d, want %d.%d",
				c.in, got.Major, got.Minor, c.wantMajor, c.wantMinor)
		}
	}
}

// TestParseVersion_PatchDoesNotAffectString pins the spec's rule that a patch
// release is not distinguishable after parsing. If String() rendered a patch,
// something downstream would eventually compare it and reject a compatible peer.
func TestParseVersion_PatchDoesNotAffectString(t *testing.T) {
	withPatch, err := ParseVersion("1.0.7")
	if err != nil {
		t.Fatalf("parse 1.0.7: %v", err)
	}
	withoutPatch, err := ParseVersion("1.0")
	if err != nil {
		t.Fatalf("parse 1.0: %v", err)
	}
	if withPatch.String() != withoutPatch.String() {
		t.Errorf("1.0.7 and 1.0 are the same protocol version, but render as %q and %q",
			withPatch.String(), withoutPatch.String())
	}
	if withPatch.Compare(withoutPatch) != 0 {
		t.Errorf("1.0.7 must not compare unequal to 1.0; patch does not affect compatibility")
	}
}

func TestVersionCompare(t *testing.T) {
	mustParse := func(s string) Version {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "1.1", -1},
		{"1.1", "1.0", 1},
		{"1.0", "2.0", -1},
		{"2.0", "1.9", 1},
		{"0.9", "1.0", -1},
		{"1.0.9", "1.0.1", 0},
	}
	for _, c := range cases {
		got := mustParse(c.a).Compare(mustParse(c.b))
		if got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestNegotiate_PicksHighestCommon is the core selection rule.
func TestNegotiate_PicksHighestCommon(t *testing.T) {
	ours := []Version{{1, 0}, {0, 3}}
	client := []Version{{0, 3}}

	got, err := Negotiate(client, ours)
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if got.Compare(Version{0, 3}) != 0 {
		t.Errorf("negotiated %s, want 0.3 (the only common version)", got)
	}

	// With both in common, the higher wins, so an upgraded peer is not dragged
	// down to the oldest shared version.
	client = []Version{{0, 3}, {1, 0}}
	got, err = Negotiate(client, ours)
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if got.Compare(Version{1, 0}) != 0 {
		t.Errorf("negotiated %s, want 1.0 (highest common)", got)
	}
}

// TestNegotiate_DisjointIsVersionNotSupported is the explicit-failure rule.
//
// There is no default and no fallback: a version the agent is not speaking must
// fail at the handshake, not be assumed.
func TestNegotiate_DisjointIsVersionNotSupported(t *testing.T) {
	ours := []Version{{1, 0}}

	_, err := Negotiate([]Version{{2, 0}, {3, 0}}, ours)
	if err == nil {
		t.Fatal("no common version must fail, not silently choose one")
	}
	if !errors.Is(err, ErrVersionNotSupported) {
		t.Errorf("a disjoint handshake must be ErrVersionNotSupported (the SDK sentinel), got: %v", err)
	}
}

// TestNegotiate_EmptyClientHeaderUsesOurs covers the 0.3 client, which sends no
// A2A-Version header. The spec's intent is "older client, offer your newest", not
// "client supports nothing".
func TestNegotiate_EmptyClientHeaderUsesOurs(t *testing.T) {
	ours := []Version{{1, 0}, {0, 3}}

	got, err := Negotiate(nil, ours)
	if err != nil {
		t.Fatalf("an absent client version must be accepted, got: %v", err)
	}
	if got.Compare(Version{1, 0}) != 0 {
		t.Errorf("negotiated %s, want our newest (1.0)", got)
	}
}

func TestNegotiate_NoOursIsAnError(t *testing.T) {
	if _, err := Negotiate([]Version{{1, 0}}, nil); err == nil {
		t.Fatal("advertising no versions at all must be an error, not a silent success")
	}
}

// TestNegotiator_MalformedIsNotUnsupported is the distinction this package keeps.
//
// "bad input" and "valid version we do not speak" must not collapse: the first is
// the client's bug and the second is ours to fix by adding support. Reporting a
// typo as VERSION_NOT_SUPPORTED would send an operator to add a version that does
// not exist.
func TestNegotiator_MalformedIsNotUnsupported(t *testing.T) {
	n, err := NewNegotiator("1.0")
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}

	for _, bad := range []string{"garbage", "1", "1.0.0.0", "a.b"} {
		_, err := n.Negotiate(bad)
		if err == nil {
			t.Errorf("Negotiate(%q) must fail", bad)
			continue
		}
		if errors.Is(err, ErrVersionNotSupported) {
			t.Errorf("Negotiate(%q) reported VERSION_NOT_SUPPORTED for malformed input; "+
				"a parse failure is the client's bug, not a version we must add", bad)
		}
	}

	// The other direction: a well-formed version we do not speak IS unsupported.
	if _, err := n.Negotiate("9.9"); !errors.Is(err, ErrVersionNotSupported) {
		t.Errorf("a well-formed unknown version must be ErrVersionNotSupported, got: %v", err)
	}
}

// TestNegotiator_HeaderForm covers the actual header value: a comma-separated
// list, which is how A2A-Extensions already works, and an empty 0.3 header.
func TestNegotiator_HeaderForm(t *testing.T) {
	n, err := NewNegotiator("1.0", "0.3")
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}

	cases := []struct {
		header    string
		want      Version
		wantErr   bool
		wantUnsup bool
	}{
		{header: "1.0", want: Version{1, 0}},
		{header: "0.3", want: Version{0, 3}},
		{header: "0.3,1.0", want: Version{1, 0}},
		{header: " 0.3 , 1.0 ", want: Version{1, 0}},
		{header: "1.0.9", want: Version{1, 0}, wantErr: false},
		{header: "", want: Version{1, 0}}, // 0.3 client, no header
		{header: "2.0", wantErr: true, wantUnsup: true},
		{header: "1.0,,0.3", wantErr: true},
	}

	for _, c := range cases {
		got, err := n.Negotiate(c.header)
		if c.wantErr {
			if err == nil {
				t.Errorf("Negotiate(%q) must fail, got %s", c.header, got)
				continue
			}
			if c.wantUnsup != errors.Is(err, ErrVersionNotSupported) {
				t.Errorf("Negotiate(%q) unsupported=%v, want %v: %v",
					c.header, errors.Is(err, ErrVersionNotSupported), c.wantUnsup, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Negotiate(%q) failed: %v", c.header, err)
			continue
		}
		if got.Compare(c.want) != 0 {
			t.Errorf("Negotiate(%q) = %s, want %s", c.header, got, c.want)
		}
	}
}

func TestNewNegotiator_RejectsMalformedAdvertisedVersion(t *testing.T) {
	if _, err := NewNegotiator("1.0", "not-a-version"); err == nil {
		t.Fatal("advertising a version we cannot parse must fail when the list is built")
	}
	if _, err := NewNegotiator(); err == nil {
		t.Fatal("an empty supported list must fail")
	}
}

func TestNegotiator_SupportedIsNewestFirst(t *testing.T) {
	n, err := NewNegotiator("0.3", "2.0", "1.0")
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}
	got := n.Supported()
	want := []Version{{2, 0}, {1, 0}, {0, 3}}
	if len(got) != len(want) {
		t.Fatalf("Supported() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Compare(want[i]) != 0 {
			t.Errorf("Supported()[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestPrimitivesAreTheSDKs guards the whole point of this package.
//
// The plan forbids inventing a negotiation primitive. These values must be the
// SDK's, not copies that could drift from the standard's spelling — a drifted
// header name would be invisible until two implementations failed to handshake.
func TestPrimitivesAreTheSDKs(t *testing.T) {
	if HeaderVersion != "A2A-Version" {
		t.Errorf("HeaderVersion = %q; the A2A service parameter is %q", HeaderVersion, "A2A-Version")
	}
	if HeaderExtensions != "A2A-Extensions" {
		t.Errorf("HeaderExtensions = %q; the A2A service parameter is %q", HeaderExtensions, "A2A-Extensions")
	}
	if ErrVersionNotSupported == nil {
		t.Fatal("ErrVersionNotSupported must be the SDK sentinel, not nil")
	}

	// The alias must be usable as the SDK type: this is what lets an unmodified
	// A2A client read a RelayFirst agent card.
	var iface AgentInterface
	iface.URL = "https://node.example/a2a"
	iface.ProtocolBinding = TransportJSONRPC
	iface.ProtocolVersion = "1.0"
	if iface.ProtocolBinding != "JSONRPC" {
		t.Errorf("TransportJSONRPC = %q, want the SDK's %q", iface.ProtocolBinding, "JSONRPC")
	}
}
