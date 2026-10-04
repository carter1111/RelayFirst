package a2a

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Version is an A2A protocol version as the spec defines it: a major and a minor.
//
// # Why patch numbers are dropped, not stored
//
// The spec is explicit that patch releases do not affect protocol compatibility
// and MUST NOT be considered during negotiation. Keeping a patch field would
// invite a caller to compare it, which is the exact bug the spec warns about:
// a client on 1.0.7 and an agent on 1.0.2 are compatible, and any code that
// thinks otherwise will reject a peer it should have accepted.
//
// So a version string with a patch component parses, but the patch is discarded.
// That is deliberate and is covered by a test, because "silently ignored" and
// "forgotten" look identical in a diff.
type Version struct {
	Major int
	Minor int
}

// ParseVersion reads an "M.m" or "M.m.p" string. The patch component, when
// present, is accepted and ignored per the spec.
//
// It is a lenient-aware parser, not a lenient one: it accepts the shapes the
// spec permits and rejects everything else, so a typo surfaces as malformed
// rather than as a version that quietly never matches. This is why callers get a
// plain error here and a distinct ErrVersionNotSupported from negotiation —
// those are two different failures with two different fixes.
func ParseVersion(s string) (Version, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Version{}, fmt.Errorf("empty version string")
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, fmt.Errorf("version %q must be M.m or M.m.p", s)
	}

	major, err := parseComponent("major", parts[0], s)
	if err != nil {
		return Version{}, err
	}
	minor, err := parseComponent("minor", parts[1], s)
	if err != nil {
		return Version{}, err
	}
	if len(parts) == 3 {
		// Validate the patch so "1.0.x" is malformed rather than silently 1.0,
		// then drop it. See the type comment: compatibility ignores it.
		if _, err := parseComponent("patch", parts[2], s); err != nil {
			return Version{}, err
		}
	}
	return Version{Major: major, Minor: minor}, nil
}

func parseComponent(name, field, whole string) (int, error) {
	if field == "" {
		return 0, fmt.Errorf("version %q has an empty %s component", whole, name)
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("version %q %s component %q is not a number", whole, name, field)
	}
	if n < 0 {
		return 0, fmt.Errorf("version %q %s component must not be negative", whole, name)
	}
	return n, nil
}

// String renders the version in the spec's M.m form, without a patch component.
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

// Compare orders two versions by major then minor. Patch is not represented, so
// it cannot affect the result.
//
// It returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	if v.Major != o.Major {
		if v.Major < o.Major {
			return -1
		}
		return 1
	}
	switch {
	case v.Minor < o.Minor:
		return -1
	case v.Minor > o.Minor:
		return 1
	default:
		return 0
	}
}

// Negotiate chooses the version to use, given what each side supports.
//
// # The rule
//
// The spec identifies a version by major.minor and calls patch irrelevant. Two
// sides are compatible when they agree on both components, so the selection is
// the highest common major.minor — highest, so a peer that upgrades does not drag
// an already-upgraded peer back down.
//
// # What it deliberately does not do
//
// It does not fall back to a default when there is no overlap. The plan (§2.6)
// requires an explicit failure: silently using some default would let a client
// believe it is speaking a version the agent is not, and the resulting confusion
// would surface later as data corruption rather than as a handshake error.
//
// An EMPTY client list is treated as a request to use ours. The spec says a
// 0.3 client sends no header, so an empty list means "older client, offer your
// newest" rather than "client supports nothing".
func Negotiate(client, ours []Version) (Version, error) {
	if len(ours) == 0 {
		return Version{}, fmt.Errorf("a2a: this side advertises no supported versions")
	}
	if len(client) == 0 {
		return highest(ours), nil
	}

	best, found := Version{}, false
	for _, c := range client {
		for _, o := range ours {
			if c.Compare(o) != 0 {
				continue
			}
			if !found || c.Compare(best) > 0 {
				best, found = c, true
			}
		}
	}
	if !found {
		return Version{}, fmt.Errorf(
			"client supports [%s], this agent supports [%s]: %w",
			formatVersions(client), formatVersions(ours), ErrVersionNotSupported)
	}
	return best, nil
}

// Negotiator holds the versions this side supports, so callers do not re-parse
// the same list on every request.
type Negotiator struct {
	supported []Version
}

// NewNegotiator parses this side's advertised versions. Any malformed entry is an
// error: advertising a version we cannot name would make the handshake
// unreliable for every peer, so it fails when the list is built, not per request.
func NewNegotiator(versions ...string) (*Negotiator, error) {
	if len(versions) == 0 {
		return nil, fmt.Errorf("a2a: a negotiator needs at least one version")
	}
	parsed := make([]Version, 0, len(versions))
	for _, raw := range versions {
		v, err := ParseVersion(raw)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, v)
	}
	return &Negotiator{supported: parsed}, nil
}

// Supported returns a copy of the versions this side supports, newest first.
func (n *Negotiator) Supported() []Version {
	out := append([]Version(nil), n.supported...)
	sort.Slice(out, func(i, j int) bool { return out[j].Compare(out[i]) < 0 })
	return out
}

// Negotiate runs the handshake against a client's advertised versions.
//
// header is the raw A2A-Version value: a comma-separated list, or empty when the
// client sent no header (the 0.3 case). It returns a plain parse error for a
// malformed header and ErrVersionNotSupported for a well-formed but disjoint one,
// so a caller can tell "fix your client" from "add this version".
func (n *Negotiator) Negotiate(header string) (Version, error) {
	client, err := parseVersionList(header)
	if err != nil {
		return Version{}, err
	}
	return Negotiate(client, n.supported)
}

// parseVersionList splits the A2A-Version header value. An empty header yields an
// empty list, which Negotiate reads as the 0.3 "use your newest" case.
func parseVersionList(header string) ([]Version, error) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return nil, nil
	}
	fields := strings.Split(trimmed, ",")
	out := make([]Version, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			return nil, fmt.Errorf("version list %q has an empty entry", header)
		}
		v, err := ParseVersion(f)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func highest(versions []Version) Version {
	best := versions[0]
	for _, v := range versions[1:] {
		if v.Compare(best) > 0 {
			best = v
		}
	}
	return best
}

func formatVersions(versions []Version) string {
	parts := make([]string, len(versions))
	for i, v := range versions {
		parts[i] = v.String()
	}
	return strings.Join(parts, ", ")
}
