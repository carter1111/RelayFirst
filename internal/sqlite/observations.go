package sqlite

import (
	"fmt"
	"time"
)

// ObservationStore indexes what receipts observed, so the work is findable (S10-1).
//
// # What this is, and the trust position it takes
//
// The node parses a receipt's JSON to extract a few indexing fields and records them.
// It does NOT verify the signature, does not check the chain, and does not decide whether
// the claim is true. That is not a shortcut: it is what an index IS.
//
// The reasoning is worth stating because "the indexer does not verify" sounds like a hole
// and is not. A search engine indexes pages without vouching for them. The index answers
// "where can I find X", and the consumer verifies what it retrieves. A forged receipt gets
// indexed too, and a consumer that checks the signature discards it — which is exactly what
// would happen if the node had never indexed it, except now the honest ones are findable.
//
// The alternative — having the node verify — would make the node an authority, which
// MVP.md §7.1 rules out structurally: the node cannot link signing code, so it *cannot*
// verify, by construction rather than by policy.
//
// # Why the index holds no verdict
//
// There is no "valid" column, deliberately. A boolean there would be a judgement the node
// is in no position to make, and a consumer reading it would be trusting the node's
// opinion. What the index records is what was claimed, attributed to who claimed it.
type ObservationStore struct {
	db *DB
}

// NewObservationStore wraps db.
func NewObservationStore(db *DB) *ObservationStore { return &ObservationStore{db: db} }

// Observation is one indexed receipt, reduced to the fields a query needs.
//
// # Why these fields and no others
//
// Each is a JSON path the node can read without understanding the receipt, and each
// answers a question a consumer actually asks: which URL was observed (Subject), what was
// claimed about it (ContentHash, ResultHash), who claimed it (AgentID), and when (Epoch).
// Anything more would mean the index had an opinion about the work.
type Observation struct {
	// ReceiptID is the receipt's own id, which is the observation's identity.
	ReceiptID string

	// Subject is the URL the task operated on. It is the primary query key.
	Subject string

	// ContentHash is the first anchor's content hash — what the observer says it saw.
	ContentHash string

	// ResultHash is the claimed result, so a consumer can compare claims without
	// fetching every receipt.
	ResultHash string

	// AgentID is who submitted the claim.
	AgentID string

	// TaskType is probe/extract/compute.
	TaskType string

	// Epoch is the mining epoch.
	Epoch uint64

	// IndexedAt is when this node recorded it.
	IndexedAt time.Time
}

// Put indexes an observation.
//
// # Why this is idempotent rather than an error on repeat
//
// The same receipt can arrive from more than one path — a direct publish, a retry after a
// lost acknowledgement, or a future relay hand-off. A duplicate must not fail, and it must
// not create a second row either: two rows for one receipt would make the cross-verification
// count wrong, and that count is the one number a consumer reads.
func (s *ObservationStore) Put(o Observation) error {
	if o.ReceiptID == "" {
		return fmt.Errorf("store: observation has no receipt id")
	}
	if o.Subject == "" {
		// An observation with no subject cannot be found by the query this table exists
		// for, so indexing it would only add noise to counts.
		return fmt.Errorf("store: observation %s has no subject", o.ReceiptID)
	}
	at := o.IndexedAt
	if at.IsZero() {
		at = time.Now()
	}

	_, err := s.db.handle.Exec(`
		INSERT INTO observations
			(receipt_id, subject, content_hash, result_hash, agent_id, task_type, epoch, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO NOTHING
	`, o.ReceiptID, o.Subject, o.ContentHash, o.ResultHash, o.AgentID, o.TaskType, o.Epoch, TimeToUnix(at))
	if err != nil {
		return fmt.Errorf("store: put observation: %w", err)
	}
	return nil
}

// BySubject returns observations of a subject, newest epoch first.
//
// A limit of zero or less means "every observation". The cap exists so a query cannot be
// made to read an unbounded amount as a popular URL accumulates.
func (s *ObservationStore) BySubject(subject string, limit int) ([]Observation, error) {
	if subject == "" {
		return nil, fmt.Errorf("store: subject is empty")
	}
	query := `SELECT receipt_id, subject, content_hash, result_hash, agent_id, task_type, epoch, indexed_at
	          FROM observations WHERE subject = ? ORDER BY epoch DESC, receipt_id`
	args := []any{subject}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.scan(query, args...)
}

// ByReceipt returns one observation.
func (s *ObservationStore) ByReceipt(receiptID string) (Observation, bool, error) {
	if receiptID == "" {
		return Observation{}, false, fmt.Errorf("store: receipt id is empty")
	}
	rows, err := s.scan(`SELECT receipt_id, subject, content_hash, result_hash, agent_id, task_type, epoch, indexed_at
	                     FROM observations WHERE receipt_id = ?`, receiptID)
	if err != nil {
		return Observation{}, false, err
	}
	if len(rows) == 0 {
		return Observation{}, false, nil
	}
	return rows[0], true, nil
}

// CrossVerification is how many distinct agents independently reported a content hash for
// one subject (S10-2).
//
// # What the counts mean, and what they do not
//
// Independent agreement is evidence, and the word doing the work is "independent": two
// receipts from one agent are one claim repeated, which is why the count is over DISTINCT
// agents and not over rows. The node reports the agreement; it does not conclude that the
// agreement is correct. Two colluding agents agree too.
//
// So this is a signal to look at, not a verdict — and every response says so.
type CrossVerification struct {
	// Subject is what was observed.
	Subject string

	// ContentHash is the claim being compared.
	ContentHash string

	// DistinctAgents is how many different agents reported this content hash.
	DistinctAgents int

	// Observations is how many receipts support it, which can exceed DistinctAgents when
	// one agent submitted more than once.
	Observations int

	// ReceiptIDs are the receipts supporting this hash, so a consumer can fetch and
	// verify each rather than trusting the count.
	ReceiptIDs []string
}

// EvidenceFor groups a subject's observations by claimed content hash.
//
// # Why grouped by content hash rather than by subject
//
// The interesting question is not "who looked at this URL" but "who agrees on what they
// saw". Grouping by hash is what makes disagreement visible: two groups for one subject
// means the observers saw different things, which is either a real change over time or a
// disagreement worth investigating. A flat list would hide that behind a count.
func (s *ObservationStore) EvidenceFor(subject string, limit int) ([]CrossVerification, error) {
	obs, err := s.BySubject(subject, limit)
	if err != nil {
		return nil, err
	}

	// Group by content hash, keeping insertion order stable by sorting at the end.
	byHash := map[string]*CrossVerification{}
	agents := map[string]map[string]struct{}{}
	var order []string

	for _, o := range obs {
		if o.ContentHash == "" {
			// A receipt with no content hash cannot be compared, so it belongs in no group.
			// Silently skipping it is better than a group named "" that a consumer would
			// read as a claim.
			continue
		}
		g, ok := byHash[o.ContentHash]
		if !ok {
			g = &CrossVerification{Subject: subject, ContentHash: o.ContentHash}
			byHash[o.ContentHash] = g
			agents[o.ContentHash] = map[string]struct{}{}
			order = append(order, o.ContentHash)
		}
		g.Observations++
		g.ReceiptIDs = append(g.ReceiptIDs, o.ReceiptID)
		agents[o.ContentHash][o.AgentID] = struct{}{}
	}

	out := make([]CrossVerification, 0, len(order))
	for _, h := range order {
		g := byHash[h]
		g.DistinctAgents = len(agents[h])
		out = append(out, *g)
	}

	// Most-supported first, with the hash as a tiebreak so the order is total and stable.
	// A non-deterministic order would make two identical queries look different.
	sortBySupport(out)
	return out, nil
}

// Count returns how many observations are indexed.
func (s *ObservationStore) Count() int {
	var n int
	if err := s.db.handle.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&n); err != nil {
		return 0
	}
	return n
}

func (s *ObservationStore) scan(query string, args ...any) ([]Observation, error) {
	rows, err := s.db.handle.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query observations: %w", err)
	}
	defer rows.Close()

	var out []Observation
	for rows.Next() {
		var (
			o         Observation
			indexedAt int64
		)
		if err := rows.Scan(&o.ReceiptID, &o.Subject, &o.ContentHash, &o.ResultHash,
			&o.AgentID, &o.TaskType, &o.Epoch, &indexedAt); err != nil {
			return nil, fmt.Errorf("store: scan observation: %w", err)
		}
		o.IndexedAt = UnixToTime(indexedAt)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate observations: %w", err)
	}
	return out, nil
}

// sortBySupport orders groups by distinct agents, then observations, then hash.
//
// The hash tiebreak is what makes the order total: two groups with identical counts would
// otherwise come back in map order, which changes between runs.
func sortBySupport(groups []CrossVerification) {
	for i := 1; i < len(groups); i++ {
		for j := i; j > 0; j-- {
			a, b := groups[j-1], groups[j]
			if a.DistinctAgents > b.DistinctAgents {
				break
			}
			if a.DistinctAgents == b.DistinctAgents && a.Observations > b.Observations {
				break
			}
			if a.DistinctAgents == b.DistinctAgents && a.Observations == b.Observations &&
				a.ContentHash < b.ContentHash {
				break
			}
			groups[j-1], groups[j] = groups[j], groups[j-1]
		}
	}
}
