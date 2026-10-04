package publish

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
)

// Multi-relay policy (P1 #4).
//
// # What was missing
//
// `Publish` already fanned out concurrently and reported per-relay outcomes, and one
// acknowledgement was treated as success. That is the right default for "did my message get
// somewhere", and it is not enough for the things a permissionless network actually needs:
//
//   - QUORUM. "One node took it" and "the network took it" are different claims, and a caller
//     that cannot distinguish them cannot decide whether the work is durable. Two acknowledgements
//     from the same operator's two front-ends are also not two, which is why quorum counts
//     INDEPENDENT relays and lets a caller say what independent means.
//   - HEALTH. A relay that times out every time should stop being tried first, or every
//     publication pays its timeout. But health must not become a silent exclusion: a relay that is
//     merely slow today must come back, and a relay is only excluded when the caller asked for it.
//   - FAILOVER. After a failure, trying the next candidate is what makes a relay set useful. It
//     must be bounded, because an unbounded retry against a dead set is a hang.
//
// # What this is not
//
// It is not consensus. NET-3 says relays need not agree, and nothing here asks them to: quorum
// counts acknowledgements of DELIVERY, which is a local fact each node can answer alone. A caller
// must not read "quorum reached" as "the network agrees on anything".

// QuorumPolicy decides whether an outcome counts as delivered.
type QuorumPolicy struct {
	// Required is how many acknowledgements are needed. Zero means any one (the historical
	// default).
	Required int

	// Of is the denominator for a proportional requirement, e.g. Required=0, Fraction=0.5.
	// Zero means no proportional requirement.
	Fraction float64

	// IndependentOf groups relays so that quorum counts GROUPS rather than relays.
	//
	// # Why this exists
	//
	// One operator running three front-ends to the same database is one place to lose the
	// message, not three. Counting them as three would let a single operator satisfy a quorum by
	// itself, which defeats the point of asking for one. The map is relay label to group name, so
	// a caller can say "these are the same operator" without this package needing to know how that
	// was determined.
	//
	// A relay absent from the map is its own group, which is the safe default: an unlabelled relay
	// counts as one independent holder rather than being silently merged with another.
	IndependentOf map[string]string
}

// Satisfied reports whether an outcome meets the policy.
//
// It returns the count and the requirement so a caller can report WHY it fell short. A bare
// boolean would leave an operator unable to tell "no node answered" from "one answered and two
// were required", and those need different responses.
func (p QuorumPolicy) Satisfied(out Outcome) (reached int, required int, ok bool) {
	groups := map[string]bool{}
	for _, r := range out.Results {
		if !r.OK {
			continue
		}
		groups[groupOf(p.IndependentOf, r.Relay)] = true
	}
	reached = len(groups)

	if p.Fraction > 0 {
		// The fraction is over the number of relays ATTEMPTED, not configured: a caller that
		// failed over to a subset should be measured against what it tried.
		attempted := len(out.Results)
		need := int(float64(attempted)*p.Fraction + 0.999999)
		if need < 1 {
			need = 1
		}
		required = need
		if p.Required > required {
			required = p.Required
		}
	} else {
		required = p.Required
	}
	if required < 1 {
		required = 1
	}
	return reached, required, reached >= required
}

func groupOf(groups map[string]string, relay string) string {
	if g, ok := groups[relay]; ok && g != "" {
		return g
	}
	return relay
}

// RelayHealth tracks how each relay has behaved, so a caller can order attempts sensibly.
//
// # Why the state is explicit rather than inferred from the last attempt
//
// A single timeout is not a sick relay: networks hiccup, and a node restarts. Excluding on one
// failure would drop a good relay for a transient reason, and with few relays that is a
// availability loss. So failures accumulate and recovery is possible, with the thresholds the
// caller's.
type RelayHealth struct {
	mu sync.Mutex

	// FailThreshold is how many consecutive failures mark a relay unhealthy.
	FailThreshold int

	// RecoveryWindow is how long an unhealthy relay is avoided before being retried.
	//
	// # Why a window rather than permanent exclusion
	//
	// Permanent exclusion turns one bad minute into a permanently reduced relay set, and the caller
	// has no way to notice it happened. A window means a relay is retried periodically, so a
	// recovered node rejoins without anyone acting, and a dead one costs one attempt per window.
	RecoveryWindow time.Duration

	state map[string]*relayState
}

type relayState struct {
	consecutiveFailures int
	lastFailure         time.Time
	lastSuccess         time.Time
	totalFailures       int
	totalSuccesses      int
}

// NewRelayHealth returns health tracking with sane defaults.
func NewRelayHealth() *RelayHealth {
	return &RelayHealth{
		FailThreshold:  3,
		RecoveryWindow: 60 * time.Second,
		state:          map[string]*relayState{},
	}
}

// Record notes the result of one attempt.
func (h *RelayHealth) Record(relay string, ok bool, at time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil {
		h.state = map[string]*relayState{}
	}
	st, exists := h.state[relay]
	if !exists {
		st = &relayState{}
		h.state[relay] = st
	}
	if ok {
		st.consecutiveFailures = 0
		st.lastSuccess = at
		st.totalSuccesses++
		return
	}
	st.consecutiveFailures++
	st.lastFailure = at
	st.totalFailures++
}

// Healthy reports whether a relay should be attempted first, and why not when it should not.
//
// # Why the reason is returned
//
// "This relay is being avoided" is the kind of decision that becomes invisible and then
// surprising. Returning the reason lets an operator see it in a report instead of discovering a
// relay was quietly skipped.
func (h *RelayHealth) Healthy(relay string, now time.Time) (bool, string) {
	if h == nil {
		return true, ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.state[relay]
	if !ok {
		// Never tried: attempt it. Treating an unknown relay as unhealthy would mean a fresh
		// process never tries anything.
		return true, ""
	}
	threshold := h.FailThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if st.consecutiveFailures < threshold {
		return true, ""
	}
	window := h.RecoveryWindow
	if window <= 0 {
		window = 60 * time.Second
	}
	if now.Sub(st.lastFailure) >= window {
		// The window has passed, so retry. A recovered relay rejoins without intervention.
		return true, ""
	}
	return false, fmt.Sprintf("%d consecutive failures; avoided for another %s",
		st.consecutiveFailures, (window - now.Sub(st.lastFailure)).Round(time.Second))
}

// Snapshot reports each relay's counters, sorted by label for stable output.
func (h *RelayHealth) Snapshot() []RelayHealthEntry {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RelayHealthEntry, 0, len(h.state))
	for relay, st := range h.state {
		out = append(out, RelayHealthEntry{
			Relay:               relay,
			ConsecutiveFailures: st.consecutiveFailures,
			TotalFailures:       st.totalFailures,
			TotalSuccesses:      st.totalSuccesses,
			LastFailure:         st.lastFailure,
			LastSuccess:         st.lastSuccess,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Relay < out[j].Relay })
	return out
}

// RelayHealthEntry is one relay's counters.
type RelayHealthEntry struct {
	Relay               string
	ConsecutiveFailures int
	TotalFailures       int
	TotalSuccesses      int
	LastFailure         time.Time
	LastSuccess         time.Time
}

// FailoverPolicy bounds how many relays to try.
type FailoverPolicy struct {
	// MaxAttempts caps the relays tried in one publication.
	//
	// # Why a cap is required rather than optional
	//
	// An unbounded failover over a dead set is a hang: each attempt costs up to a timeout, so N
	// dead relays means N timeouts. A cap makes the worst case a number the caller chose.
	//
	// Zero means try every configured relay, which is the historical behaviour.
	MaxAttempts int

	// MinAcked stops early once this many relays have acknowledged.
	//
	// A caller that only needs one acknowledgement should not pay for the rest. Zero means run to
	// the cap.
	MinAcked int
}

// PublishWithPolicy delivers an envelope under a quorum, health and failover policy.
//
// # The order of operations, and why
//
//  1. Order candidates: healthy first, then configured order. A known-sick relay is tried last
//     rather than skipped, so quorum can still be reached when the healthy ones are insufficient.
//  2. Attempt up to the failover cap, stopping early once MinAcked is reached.
//  3. Record health for each attempt, so the next publication is better informed.
//  4. Evaluate quorum over what actually happened.
//
// # What is returned when quorum fails
//
// An OUTCOME, not an error. Falling short of a quorum is a delivery result the caller asked to be
// told about — the same reasoning as a partial fan-out. An error is reserved for the call being
// impossible, such as no relays being configured.
func (p *Publisher) PublishWithPolicy(
	ctx context.Context,
	env node.Envelope,
	quorum QuorumPolicy,
	health *RelayHealth,
	failover FailoverPolicy,
	now func() time.Time,
) (Outcome, error) {
	if p == nil || len(p.Relays) == 0 {
		return Outcome{}, fmt.Errorf("publish: no relays configured")
	}
	if now == nil {
		now = time.Now
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}

	ordered := orderByHealth(p.Relays, health, now())

	max := failover.MaxAttempts
	if max <= 0 || max > len(ordered) {
		max = len(ordered)
	}

	out := Outcome{Results: make([]RelayResult, 0, max)}
	for i := 0; i < max; i++ {
		relay := ordered[i]
		res := p.deliver(ctx, client, relay, env)
		out.Results = append(out.Results, res)
		if res.OK {
			out.Acked++
		} else {
			out.Failed++
		}
		if health != nil {
			health.Record(relay.label(), res.OK, now())
		}
		if failover.MinAcked > 0 && out.Acked >= failover.MinAcked {
			// Enough acknowledgements. Stopping here is the point of MinAcked: a caller that needs
			// one should not pay for the rest.
			break
		}
	}

	reached, required, ok := quorum.Satisfied(out)
	out.QuorumReached = reached
	out.QuorumRequired = required
	out.QuorumMet = ok
	return out, nil
}

// orderByHealth puts healthy relays first, preserving configured order within each group.
//
// # Why unhealthy relays are kept rather than dropped
//
// Dropping them would reduce the relay set the caller configured, silently and permanently for the
// duration of the window. Keeping them at the back means quorum can still be reached when the
// healthy ones fall short, and the ordering only expresses a preference.
func orderByHealth(relays []Relay, health *RelayHealth, now time.Time) []Relay {
	out := append([]Relay(nil), relays...)
	if health == nil {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		hi, _ := health.Healthy(out[i].label(), now)
		hj, _ := health.Healthy(out[j].label(), now)
		// Stable sort keeps configured order within the healthy and unhealthy groups.
		return hi && !hj
	})
	return out
}

// HealthReport renders a human-readable view of relay health.
func HealthReport(entries []RelayHealthEntry) string {
	if len(entries) == 0 {
		return "no relay health recorded yet"
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s: %d ok / %d failed", e.Relay, e.TotalSuccesses, e.TotalFailures)
		if e.ConsecutiveFailures > 0 {
			fmt.Fprintf(&b, " (%d consecutive)", e.ConsecutiveFailures)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
