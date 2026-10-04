package scoring

import (
	"fmt"
	"sync"
	"time"
)

// Sighting records the first time an artifact was seen anywhere on the network.
type Sighting struct {
	// ArtifactKey is the ledger key. It never includes an agent id: that is what
	// makes the ledger global rather than per-agent (invariant A6).
	ArtifactKey string

	// FirstSeenAt is when the artifact was first submitted.
	FirstSeenAt time.Time

	// FirstAgentID is the agent that got there first. Stored as attribution only
	// — it is not part of the key and grants no ongoing privilege.
	FirstAgentID string

	// SeenCount counts *credited* submissions of this artifact, including the
	// first. It deliberately does NOT count rejected attempts.
	//
	// That distinction is a security property, not a bookkeeping detail. If
	// unverified submissions were recorded, an attacker could "burn" an artifact
	// by submitting a garbage receipt naming it: the key would be marked seen and
	// the agent who later does the real work would be denied novelty. Only
	// validated, earning observations reach this ledger, so claiming a key
	// requires actually reproducing the content.
	SeenCount uint64
}

// Ledger records which artifacts have already been observed.
//
// Implementations MUST be global: keyed by artifact alone. Keying by
// (agent, artifact) would let every agent claim novelty for the same content,
// which is exactly the farming strategy the MVP has to defeat (MVP.md §5.2).
//
// The interface exists so the in-memory implementation can be swapped for the
// SQLite-backed one once S2-8 lands, without touching callers.
type Ledger interface {
	// Observe records a submission and reports whether this artifact was
	// previously unseen. seen is true for the first submission only.
	Observe(artifactKey, agentID string, at time.Time) (seen bool, err error)

	// Lookup returns the sighting for an artifact, if any.
	Lookup(artifactKey string) (Sighting, bool)

	// Len returns the number of distinct artifacts recorded.
	Len() int
}

// MemLedger is an in-memory Ledger.
//
// It is safe for concurrent use. Durability is deliberately not attempted: S2-8
// will provide the persistent implementation, and pretending an in-memory map
// is durable would be worse than admitting it is not.
type MemLedger struct {
	mu        sync.RWMutex
	sightings map[string]Sighting
}

// NewMemLedger returns an empty in-memory ledger.
func NewMemLedger() *MemLedger {
	return &MemLedger{sightings: make(map[string]Sighting)}
}

// Observe records a submission, reporting whether it was the first.
func (l *MemLedger) Observe(artifactKey, agentID string, at time.Time) (bool, error) {
	if artifactKey == "" {
		return false, fmt.Errorf("scoring: artifact key is empty")
	}
	if agentID == "" {
		return false, fmt.Errorf("scoring: agent id is empty")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.sightings[artifactKey]; ok {
		existing.SeenCount++
		l.sightings[artifactKey] = existing
		return false, nil
	}

	l.sightings[artifactKey] = Sighting{
		ArtifactKey:  artifactKey,
		FirstSeenAt:  at,
		FirstAgentID: agentID,
		SeenCount:    1,
	}
	return true, nil
}

// Lookup returns the sighting for an artifact.
func (l *MemLedger) Lookup(artifactKey string) (Sighting, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	s, ok := l.sightings[artifactKey]
	return s, ok
}

// Len returns the number of distinct artifacts.
func (l *MemLedger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return len(l.sightings)
}

// Snapshot returns a copy of every sighting, for auditing and tests.
func (l *MemLedger) Snapshot() []Sighting {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]Sighting, 0, len(l.sightings))
	for _, s := range l.sightings {
		out = append(out, s)
	}
	return out
}
