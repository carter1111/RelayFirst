// Package receipt defines the RelayFirst receipt — the single core data
// structure of the MVP (MVP.md §4).
//
// A receipt must let anyone independently verify that an agent really did the
// work, without trusting any RelayFirst server. That means:
//
//   - every receipt carries at least one externally re-fetchable anchor
//   - the result is deterministic and independently recomputable
//   - the signature covers all of the above
//
// See MVP.md §4 and §5.0. Task types are restricted to those with independent
// ground truth (invariant A2): probe / extract / compute.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/eip712"
	ep "github.com/relayfirst/relayfirst/internal/epoch"
)

// Schema is the receipt schema identifier.
const Schema = "relayfirst.receipt.v1"

// TaskType enumerates the accepted mining task types.
//
// Only types with an *independent ground truth* are allowed. Summary,
// classification and translation are deliberately rejected: they have no
// objective correctness criterion, so verifying them would require a dispute
// layer (MVP.md §5.0, invariant A2).
type TaskType string

const (
	// TaskProbe checks URL reachability / status code / response headers.
	TaskProbe TaskType = "probe"
	// TaskExtract pulls specified fields out of a page or API response.
	TaskExtract TaskType = "extract"
	// TaskCompute performs a deterministic computation.
	TaskCompute TaskType = "compute"
)

// Accepted reports whether t is one of the ground-truth task types.
func (t TaskType) Accepted() bool {
	switch t {
	case TaskProbe, TaskExtract, TaskCompute:
		return true
	default:
		return false
	}
}

// VerificationStatus tracks the adversarial verification outcome (MVP.md §5.5).
type VerificationStatus string

const (
	VerificationPending  VerificationStatus = "pending"
	VerificationVerified VerificationStatus = "verified"
	VerificationRejected VerificationStatus = "rejected"
)

// Receipt mirrors MVP.md §4 exactly.
type Receipt struct {
	Schema    string `json:"schema"`
	ReceiptID string `json:"receiptId"`
	AgentID   string `json:"agentId"`
	Epoch     uint64 `json:"epoch"`

	Task         Task         `json:"task"`
	Work         Work         `json:"work"`
	Result       Result       `json:"result"`
	Anchors      []Anchor     `json:"anchors"`
	Verification Verification `json:"verification"`
	Signature    string       `json:"signature"`

	// Payload, when present, is the exact byte sequence the signature covers
	// (S9-0b2, invariant A9 — see MVP.md §17.5).
	//
	// # Why this exists
	//
	// Without it a verifier rebuilds the signed bytes from the structured fields
	// above. That works only while both sides agree on the field set: add a field
	// and an older verifier rebuilds different bytes, computes a different
	// payloadHash, and rejects a receipt that is actually valid. That makes
	// "add a field" a breaking change, which is exactly what A9 forbids.
	//
	// When this field is present the verifier hashes these bytes verbatim and
	// never re-serializes, so adding fields becomes additive.
	//
	// # Why it is optional
	//
	// Every receipt signed before this field existed omits it, and those receipts
	// must keep verifying (A9 §①). When it is absent, SignedPayload falls back to
	// the structural path, which is byte-identical to what those receipts signed.
	//
	// # Why it is not signed
	//
	// It is not itself inside the signed payload — it *is* the signed payload's
	// bytes. Including it in the struct the signature covers would be circular.
	//
	// # Consumers must not treat this as authoritative metadata (finding M3)
	//
	// Its presence is not proof that the signer used it. A receipt originally
	// signed structurally can have a Payload added afterwards equal to its
	// structural bytes and still verify, because the bytes — and therefore the
	// hash — are identical. That specific case is harmless, but a verifier cannot
	// in general distinguish "this blob is what was signed" from "this blob
	// happens to hash the same".
	//
	// So the rule for consumers is: read the *validated* structured fields, not
	// this field. Validation is what guarantees the two agree (Validate rejects a
	// receipt whose fields do not match the payload), and it is the only reason
	// either can be trusted. Anything that parses the receipt without validating
	// it must not present Payload-derived values as though they were checked.
	Payload string `json:"payload,omitempty"`
}

// Task describes what was asked.
type Task struct {
	Type          TaskType       `json:"type"`
	Spec          map[string]any `json:"spec"`
	SpecHash      string         `json:"specHash"`
	SelfGenerated bool           `json:"selfGenerated"`

	// A2ATaskID is reserved for future A2A interoperability (MVP.md §16.4).
	// It stays null for the whole MVP.
	A2ATaskID *string `json:"a2aTaskId"`
}

// Work records the inference budget consumed.
type Work struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	TokensIn   uint64 `json:"tokensIn"`
	TokensOut  uint64 `json:"tokensOut"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
}

// Result is the deterministic outcome a verifier recomputes.
type Result struct {
	Value string `json:"value"`
	Hash  string `json:"hash"`
}

// Anchor is an externally re-fetchable piece of evidence. At least one is
// required; without it a receipt is just an unverifiable self-claim (MVP.md §4.1).
type Anchor struct {
	URL         string `json:"url"`
	ContentHash string `json:"contentHash"`
	FetchedAt   int64  `json:"fetchedAt"`
	Status      int    `json:"status"`
	Bytes       uint64 `json:"bytes"`
}

// Verification is filled in after the fact by a randomly assigned verifier
// (MVP.md §5.5). It is NOT part of the signed payload.
type Verification struct {
	Status         VerificationStatus `json:"status"`
	VerifierID     *string            `json:"verifierId"`
	VerifiedAt     *int64             `json:"verifiedAt"`
	Stake          uint64             `json:"stake"`
	RecomputedHash *string            `json:"recomputedHash"`
}

// signedPayload is the exact structure covered by the signature. It is a named
// type (rather than an anonymous struct) so the canonical writer can address it
// explicitly and the field order stays pinned to one declaration.
//
// Per MVP.md §4.2 the signature covers:
//
//	agentId ‖ epoch ‖ task ‖ work ‖ result ‖ anchors
//
// Note that receiptId is derived from this payload, and `verification` is filled
// in later by a verifier — including either would make the receipt unverifiable.
type signedPayload struct {
	AgentID string   `json:"agentId"`
	Epoch   uint64   `json:"epoch"`
	Task    Task     `json:"task"`
	Work    Work     `json:"work"`
	Result  Result   `json:"result"`
	Anchors []Anchor `json:"anchors"`
}

// SignedPayload returns the canonical bytes covered by the signature.
//
// # Two paths, and why both must exist
//
// If Payload is present, it *is* the signed byte sequence: hash it verbatim and
// never re-serialize. This is what makes adding a field an additive change
// rather than a breaking one (S9-0b2, A9 §④).
//
// If Payload is absent — every receipt signed before that field existed — fall
// back to rebuilding the bytes from the structured fields. That path is
// byte-identical to what those receipts signed, so they keep verifying (A9 §①).
//
// The structural path is therefore permanent, not transitional: A9 forbids
// deleting it, because deleting it would invalidate historical receipts.
func (r Receipt) SignedPayload() ([]byte, error) {
	if r.Payload != "" {
		return []byte(r.Payload), nil
	}
	return canonicalJSON(signedPayload{
		AgentID: r.AgentID,
		Epoch:   r.Epoch,
		Task:    r.Task,
		Work:    r.Work,
		Result:  r.Result,
		Anchors: r.Anchors,
	})
}

// TypedData builds the EIP-712 typed data for this receipt.
//
// The domain is chain-agnostic (MVP.md §8, ARCHITECTURE.md §3.6): signing an
// offchain receipt must never require chain knowledge.
//
// # Version profiles (S9-0h)
//
// The profile is chosen by the receipt's declared schema major, and each major's
// bytes are frozen:
//
//	v1  domain Version "1", message {agentId, epoch, payloadHash}
//	v2  domain Version "2", message {agentId, epoch, payloadHash, receiptId, schemaMajor}
//
// # Why v1 is not changed
//
// The obvious fix for "schema is unauthenticated" is to add the schema to the
// signed message. Doing that unconditionally would change the digest of every
// receipt already in existence and invalidate all of them — the exact outcome A9
// forbids. So the binding is introduced as a *new* profile: v1 keeps the bytes it
// was signed with, and v2 onwards carries the binding.
//
// That is what closes the relabel attack: presenting a v1-signed receipt as v2
// makes the verifier apply the v2 profile, whose digest differs, so the signature
// fails.
func (r Receipt) TypedData() (eip712.TypedData, error) {
	major, _, ok := ParseSchema(r.Schema)
	if !ok {
		// A malformed schema is caught by ValidateStructure before signing, so
		// reaching here means the caller skipped it. Fail loudly rather than
		// guessing a profile, because guessing is how a receipt gets signed under
		// the wrong rules.
		return eip712.TypedData{}, invalid("cannot select a signing profile for malformed schema %q", r.Schema)
	}
	return r.typedDataForMajor(major)
}

// typedDataForMajor builds the typed data for a specific profile.
//
// It is separate from TypedData so verification can ask for a profile
// independently of what the receipt claims, which is what makes the relabel
// attack detectable.
//
// # Why an unknown major is refused instead of clamped (S9-0c)
//
// The body below selects a profile with `major >= 2`. That reads as "v2 and
// everything after it", which is only correct while the newest profile is also
// the right profile for every future major — an assumption that holds exactly
// until it does not. On the day v3 changes a field, a v3 receipt would keep
// being validated under v2 rules and *pass*, because the extra field is simply
// ignored. The gate would go green on a receipt it never actually checked.
//
// Refusing unknown majors turns that silent wrong-answer into a loud
// UnsupportedError, which is the answer callers already know how to report
// (S9-0j). A build that wants to validate v3 must add v3 to supportedMajors and
// give it a profile; it cannot drift into one by accident.
func (r Receipt) typedDataForMajor(major int) (eip712.TypedData, error) {
	if !IsSupportedMajor(major) {
		return eip712.TypedData{}, unsupported(
			"no signing profile for schema major v%d (supported: %s); upgrade the verifier",
			major, formatMajors())
	}
	return r.profileForMajor(major)
}

// profileForMajor builds the typed data for a major's rules, without checking
// whether this build supports that major.
//
// The split exists so the profile *mechanism* can be tested directly. Testing it
// through typedDataForMajor would mean either shipping a fake version in
// supportedMajors or being unable to test the rules until a real v2 lands — and
// "untestable until after release" is how the relabel attack got in.
//
// Production code must not call this: it would build a signature under rules the
// build cannot claim to check. typedDataForMajor is the entry point; this is the
// part it delegates to.
func (r Receipt) profileForMajor(major int) (eip712.TypedData, error) {
	payload, err := r.SignedPayload()
	if err != nil {
		return eip712.TypedData{}, err
	}
	payloadHash := eip712.Keccak256(payload)

	types := eip712.Types{
		"RelayReceipt": {
			{Name: "agentId", Type: "string"},
			{Name: "epoch", Type: "uint64"},
			{Name: "payloadHash", Type: "bytes32"},
		},
	}

	// EIP-712 has no uint64; encode epoch as uint256.
	types["RelayReceipt"][1].Type = "uint256"

	message := map[string]any{
		"agentId":     r.AgentID,
		"epoch":       fmt.Sprintf("%d", r.Epoch),
		"payloadHash": "0x" + toHex(payloadHash),
	}

	// v1 is frozen: it must keep exactly the bytes it was signed with.
	version := "1"

	if major >= 2 {
		// Bind the two fields that were previously free-standing metadata:
		// receiptId (the key every consumer dedups on) and the schema major
		// (the version selector itself). Without these, an intermediary can
		// relabel a v1 receipt as v2, or re-emit a valid receipt under a fresh
		// id, and the signature would not notice.
		//
		// Reaching this branch for a major this build does not support is
		// impossible: the guard above returns first, so a future v3 cannot
		// inherit v2's rules by falling through.
		version = strconv.Itoa(major)
		types["RelayReceipt"] = append(types["RelayReceipt"],
			eip712.Field{Name: "receiptId", Type: "string"},
			eip712.Field{Name: "schemaMajor", Type: "uint256"},
		)
		message["receiptId"] = r.ReceiptID
		message["schemaMajor"] = fmt.Sprintf("%d", major)
	}

	return eip712.TypedData{
		Types:       types,
		PrimaryType: "RelayReceipt",
		Domain: eip712.Domain{
			Name:    "RelayFirst",
			Version: version,
		},
		Message: message,
	}, nil
}

// ReceiptIDFromPayload is the canonical derivation of a receipt id.
//
// # Why this is a correctness function, not a convenience
//
// receiptId is not inside the signed payload, so nothing about the signature
// covers it — yet it is the key that the store, the dedup ledger, points
// idempotency and verdict lookup all use. An attacker holding one valid receipt
// could re-emit it under arbitrary fresh ids and defeat every one of those
// checks, because each new id looks like a new receipt.
//
// The id is deterministic from the signed payload, so the fix does not need a
// signature change: recompute it and reject any receipt whose id does not match.
// A tampered id then fails even though the signature still verifies, and the
// check applies to v1 receipts too, which is what makes it a fix rather than a
// new-version-only mitigation.
func ReceiptIDFromPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "0x" + hex.EncodeToString(sum[:])
}

// DerivedReceiptID returns the canonical id for this receipt's signed payload.
//
// Named "derived" rather than "receiptId" to avoid colliding with the ReceiptID
// field, which is what the receipt *claims*; this is what it must equal.
func (r Receipt) DerivedReceiptID() (string, error) {
	payload, err := r.SignedPayload()
	if err != nil {
		return "", err
	}
	return ReceiptIDFromPayload(payload), nil
}

// Digest returns the EIP-712 signing digest.
func (r Receipt) Digest() ([]byte, error) {
	td, err := r.TypedData()
	if err != nil {
		return nil, err
	}
	return eip712.HashTypedData(td)
}

// Sign signs the receipt with the given private key and stores the signature.
func (r *Receipt) Sign(privKeyHex string) error {
	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		return err
	}

	td, err := r.TypedData()
	if err != nil {
		return err
	}
	sig, err := eip712.Sign(priv, td)
	if err != nil {
		return err
	}
	r.Signature = "0x" + toHex(sig)
	return nil
}

// ValidationError describes why a receipt failed validation.
type ValidationError struct {
	Reason string
}

func (e *ValidationError) Error() string { return "receipt: " + e.Reason }

func invalid(format string, args ...any) error {
	return &ValidationError{Reason: fmt.Sprintf(format, args...)}
}

// ValidateStructure checks the receipt's shape and internal consistency,
// without touching the network and without checking the signature.
//
// This is the offline half of acceptance criterion ② (MVP.md §11).
func (r Receipt) ValidateStructure() error {
	// Schema grammar, not exact match (S9-0a, invariant A9).
	//
	// The distinction matters: an unknown major returns *UnsupportedError
	// ("I cannot check this") rather than *ValidationError ("this is broken").
	// Collapsing them would make an out-of-date verifier look like it caught a
	// forgery. See schema.go.
	if err := CheckSchema(r.Schema); err != nil {
		return err
	}
	if strings.TrimSpace(r.ReceiptID) == "" {
		return invalid("receiptId is empty")
	}
	if strings.TrimSpace(r.AgentID) == "" {
		return invalid("agentId is empty")
	}

	if !r.Task.Type.Accepted() {
		return invalid("task type %q is not accepted (allowed: probe/extract/compute)", r.Task.Type)
	}
	if r.Task.Spec == nil {
		return invalid("task.spec is nil")
	}
	if strings.TrimSpace(r.Task.SpecHash) == "" {
		return invalid("task.specHash is empty")
	}

	if len(r.Anchors) == 0 {
		return invalid("at least one anchor is required")
	}
	for i, a := range r.Anchors {
		if strings.TrimSpace(a.URL) == "" {
			return invalid("anchors[%d].url is empty", i)
		}
		if err := checkHash("anchors["+itoa(i)+"].contentHash", a.ContentHash); err != nil {
			return err
		}
		if a.URL != "inline" && a.Status == 0 {
			return invalid("anchors[%d].status is unset", i)
		}
	}

	if err := checkHash("result.hash", r.Result.Hash); err != nil {
		return err
	}
	if strings.TrimSpace(r.Result.Value) == "" {
		return invalid("result.value is empty")
	}

	if r.Work.FinishedAt < r.Work.StartedAt {
		return invalid("work.finishedAt precedes work.startedAt")
	}

	if r.Signature != "" {
		if err := checkHexLen("signature", r.Signature, eip712.SignatureLength); err != nil {
			return err
		}
	}

	// When a verbatim payload is present, the structured fields above are a
	// *rendering* of it, not the signed content. Nothing above binds them: the
	// signature covers the payload bytes only.
	//
	// Without this check the two can diverge, and the failure is silent and
	// dangerous: an attacker flips result.value in the structured fields, the
	// signature still verifies (it never covered them), and every consumer that
	// reads the fields sees a forged value while the receipt looks authentic.
	//
	// So the fields must be proven consistent with the bytes that were actually
	// signed. Any mismatch is a defect in the receipt.
	if r.Payload != "" {
		if err := r.checkPayloadConsistency(); err != nil {
			return err
		}
	}

	// receiptId is outside the signed payload, so the signature does not cover it
	// — but it is the key the store, the dedup ledger, points idempotency and
	// verdict lookup all use (S9-0h, finding B2).
	//
	// Without this check an attacker holding one valid receipt can re-emit it
	// under arbitrary fresh ids. Each new id defeats the store conflict, the
	// mailbox dedup and the points idempotency guard, because each looks like a
	// different receipt — which breaks the global-dedup invariant (A6).
	//
	// The id is deterministic from the signed payload, so no signature change is
	// needed: recompute and compare. This applies to v1 receipts as well, which
	// is what makes it a fix rather than a new-version-only mitigation.
	if err := r.checkReceiptID(); err != nil {
		return err
	}

	return nil
}

// checkReceiptID recomputes the receipt id from the signed payload and rejects a
// mismatch.
//
// The recomputation is over SignedPayload, so it is a function of signed bytes
// only. A receipt whose id was swapped fails here even though its signature still
// verifies.
func (r Receipt) checkReceiptID() error {
	if strings.TrimSpace(r.ReceiptID) == "" {
		// Reported by the structural checks above; nothing to recompute against.
		return nil
	}

	want, err := r.DerivedReceiptID()
	if err != nil {
		return invalid("cannot derive receiptId: %v", err)
	}
	if !strings.EqualFold(r.ReceiptID, want) {
		return invalid(
			"receiptId %s does not match the signed payload (derived %s); "+
				"the id is a dedup key and must not be free-standing", r.ReceiptID, want)
	}
	return nil
}

// checkPayloadConsistency asserts that the structured fields match the verbatim
// signed payload.
//
// # Why byte comparison rather than field-by-field
//
// Rebuilding the payload from the structure and comparing bytes is the strictest
// available check, and it reuses exactly the canonical encoding the signer used.
// A field-by-field comparison would need updating every time a field is added,
// and the one time someone forgets is the time a field goes unchecked.
//
// The comparison is safe precisely because the structural path is unchanged: if
// the receipt was signed over these fields, the rebuild reproduces the payload.
func (r Receipt) checkPayloadConsistency() error {
	rebuilt, err := canonicalJSON(signedPayload{
		AgentID: r.AgentID,
		Epoch:   r.Epoch,
		Task:    r.Task,
		Work:    r.Work,
		Result:  r.Result,
		Anchors: r.Anchors,
	})
	if err != nil {
		return invalid("cannot rebuild payload for consistency check: %v", err)
	}

	if string(rebuilt) == r.Payload {
		return nil
	}

	// Byte equality failed. That is the expected outcome once the payload carries
	// a field this build does not know about (A9 §④): the rebuild cannot produce a
	// field it has no representation for.
	//
	// Falling back to a subset comparison keeps the check meaningful in that case
	// without weakening it. Every field this build *does* know must still match
	// the payload exactly, so a tampered field is still caught; what is tolerated
	// is only the presence of additional fields, which are themselves inside the
	// signed bytes and therefore already authenticated.
	//
	// Without this fallback the A9 promise is unreachable in practice: a v1.1
	// receipt would be rejected by a v1 verifier even though the verifier is
	// perfectly capable of checking the signature over the payload it was handed.
	if err := r.checkKnownFieldsAgainstPayload(); err != nil {
		return err
	}
	return nil
}

// checkKnownFieldsAgainstPayload compares each known field individually against
// the signed payload.
//
// It is the tolerant counterpart to the byte comparison above, used only when the
// byte comparison fails because the payload contains fields this build does not
// know. Each known field is decoded from the payload and compared, so a tampered
// field is still rejected; unknown fields are ignored because they are part of
// the signed bytes and need no separate trust.
func (r Receipt) checkKnownFieldsAgainstPayload() error {
	var signed struct {
		AgentID string   `json:"agentId"`
		Epoch   uint64   `json:"epoch"`
		Task    Task     `json:"task"`
		Work    Work     `json:"work"`
		Result  Result   `json:"result"`
		Anchors []Anchor `json:"anchors"`
	}

	// Tolerant decode: the whole point is that the payload may carry fields this
	// build does not know.
	if err := json.Unmarshal([]byte(r.Payload), &signed); err != nil {
		return invalid("payload is not a decodable signed payload: %v", err)
	}

	// Compare each known field by its canonical rendering. Re-marshalling both
	// sides avoids hand-writing an equality for every struct and keeps the
	// comparison consistent with how the bytes were produced.
	checks := []struct {
		name       string
		fromStruct any
		fromSigned any
	}{
		{"agentId", r.AgentID, signed.AgentID},
		{"epoch", r.Epoch, signed.Epoch},
		{"task", r.Task, signed.Task},
		{"work", r.Work, signed.Work},
		{"result", r.Result, signed.Result},
		{"anchors", r.Anchors, signed.Anchors},
	}

	for _, c := range checks {
		a, err := canonicalJSON(c.fromStruct)
		if err != nil {
			return invalid("cannot render %s for consistency check: %v", c.name, err)
		}
		b, err := canonicalJSON(c.fromSigned)
		if err != nil {
			return invalid("cannot render signed %s for consistency check: %v", c.name, err)
		}
		if string(a) != string(b) {
			return invalid(
				"field %s disagrees with the signed payload: the rendered value is %s but the signed bytes say %s",
				c.name, a, b)
		}
	}
	return nil
}

// Validate verifies the structure and the signature, and confirms the signer.
//
// expectedAgent, when non-nil, must equal the recovered signer address.
func (r Receipt) Validate(expectedAgent []byte) error {
	if err := r.ValidateStructure(); err != nil {
		return err
	}
	if r.Signature == "" {
		return invalid("signature is empty")
	}

	td, err := r.TypedData()
	if err != nil {
		return err
	}
	sig, err := hexDecode(strings.TrimPrefix(r.Signature, "0x"))
	if err != nil {
		return invalid("signature is not valid hex: %v", err)
	}

	recovered, err := eip712.RecoverAddress(td, sig)
	if err != nil {
		return invalid("signature recovery failed: %v", err)
	}

	declared, err := agentAddressFromID(r.AgentID)
	if err != nil {
		return err
	}
	if !equalBytes(declared, recovered) {
		return invalid("signer mismatch: agentId declares %s but signature recovers %s",
			eip712.AddressToHex(declared), eip712.AddressToHex(recovered))
	}

	if expectedAgent != nil && !equalBytes(expectedAgent, recovered) {
		return invalid("signer %s does not match expected %s",
			eip712.AddressToHex(recovered), eip712.AddressToHex(expectedAgent))
	}
	return nil
}

// agentAddressFromID extracts and validates the address from
// "agent:eip155:<chainId>:<address>".
//
// # Why the chainId is validated rather than ignored (finding L1)
//
// This used to skip straight to the address, leaving the chainId unchecked. The
// result was that an agentId with an empty, negative or non-numeric chainId
// ("agent:eip155::0x...", "agent:eip155:not-a-number:0x...") validated cleanly as
// long as the address and signature agreed. A consumer that routes or filters by
// chain would then act on a value nothing had ever parsed.
//
// The signature does not help here: the chainId is inside the signed agentId, so
// it is authenticated — authenticated as *this string*, not as a valid chain id.
// Binding is not the same as validating.
func agentAddressFromID(agentID string) ([]byte, error) {
	const prefix = "agent:eip155:"
	if !strings.HasPrefix(agentID, prefix) {
		return nil, invalid("agentId %q must start with %q", agentID, prefix)
	}
	rest := strings.TrimPrefix(agentID, prefix)
	i := strings.IndexByte(rest, ':')
	if i < 0 {
		return nil, invalid("agentId %q must have form agent:eip155:<chainId>:<address>", agentID)
	}

	chainStr := rest[:i]
	if chainStr == "" {
		return nil, invalid("agentId %q has an empty chainId", agentID)
	}
	if _, err := strconv.ParseUint(chainStr, 10, 64); err != nil {
		return nil, invalid("agentId chainId %q is not a decimal uint64: %v", chainStr, err)
	}

	addr := rest[i+1:]
	if addr == "" {
		return nil, invalid("agentId %q has an empty address", agentID)
	}
	out, err := eip712.HexToAddress(addr)
	if err != nil {
		return nil, invalid("agentId address: %v", err)
	}
	return out, nil
}

// AgentChainID returns the chainId declared in an agentId.
//
// It exists so a consumer that routes or filters by chain does not have to
// re-parse the id and risk disagreeing with validation about what the field
// contains.
func AgentChainID(agentID string) (uint64, error) {
	const prefix = "agent:eip155:"
	rest, ok := strings.CutPrefix(agentID, prefix)
	if !ok {
		return 0, invalid("agentId %q must start with %q", agentID, prefix)
	}
	i := strings.IndexByte(rest, ':')
	if i < 0 {
		return 0, invalid("agentId %q must have form agent:eip155:<chainId>:<address>", agentID)
	}
	id, err := strconv.ParseUint(rest[:i], 10, 64)
	if err != nil {
		return 0, invalid("agentId chainId %q is not a decimal uint64: %v", rest[:i], err)
	}
	return id, nil
}

// AgentID builds the canonical RelayFirst agent id.
func AgentID(chainID uint64, addr []byte) string {
	return fmt.Sprintf("agent:eip155:%d:%s", chainID, eip712.AddressToHex(addr))
}

// DeriveAgentID returns the canonical agent id for a secp256k1 private key.
//
// This is how a user's local identity is derived at `init` time (S6): the key
// never leaves the machine and no registration is required (MVP.md §10.2).
func DeriveAgentID(privKeyHex string, chainID uint64) (string, error) {
	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		return "", err
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	return AgentID(chainID, addr), nil
}

// MarshalCanonical renders the receipt as canonical JSON (deterministic field
// order), matching the byte-level output the TypeScript side must reproduce.
func (r Receipt) MarshalCanonical() ([]byte, error) { return canonicalJSON(r) }

// Unmarshal parses a receipt.
//
// # Why unknown fields are tolerated (S9-0b, A9 §④)
//
// This used to set DisallowUnknownFields, on the reasoning that "a future schema
// change cannot silently pass through the verifier". The reasoning was wrong in
// one specific way that matters once other people run this code: a receipt is not
// only parsed by the version that produced it. A verifier that rejects unknown
// fields cannot verify a receipt from a *newer* version at all, even though the
// signature over the payload is perfectly checkable — and a verifier already
// deployed to someone's machine can never be taught otherwise.
//
// So the first public release must tolerate unknown fields, or the A9 promise
// ("a receipt stays verifiable forever") fails for every receipt that carries an
// addition.
//
// # Why this is not a weakening
//
// Tolerating a field is not trusting it. Fields outside the signed payload were
// never authenticated and are ignored. Fields inside the signed payload are part
// of the bytes the signature covers, so they are authenticated by construction —
// and the consistency check in Validate still requires every field this build
// knows to match the signed bytes exactly.
//
// StrictDecode remains available for red-team and KAT tests, so the tolerance
// itself is testable rather than asserted.
func Unmarshal(data []byte) (*Receipt, error) {
	return decode(data, false)
}

// UnmarshalStrict parses a receipt and rejects unknown fields.
//
// It exists so the tolerant behaviour above is testable: a test can assert that
// the same bytes are accepted by Unmarshal and rejected by UnmarshalStrict, which
// is what makes "we tolerate unknown fields" a checked claim rather than a
// comment. Production paths use Unmarshal.
func UnmarshalStrict(data []byte) (*Receipt, error) {
	return decode(data, true)
}

func decode(data []byte, strict bool) (*Receipt, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if strict {
		dec.DisallowUnknownFields()
	}

	var r Receipt
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("receipt: decode: %w", err)
	}
	return &r, nil
}

// NewEpoch returns the epoch number for t given an epoch length.
//
// The origin is the shared epoch genesis (internal/epoch), not the unix zero.
// An unanchored index is astronomically large, so the origin has to be a fixed
// constant that receipt and scoring agree on rather than something each derives
// for itself. The epoch is part of the signed payload, so a verifier under a
// different origin would compute a different index and reject the signature —
// failing safe, but only because both sides share this one definition.
func NewEpoch(t time.Time, epochLength time.Duration) uint64 {
	return ep.Of(t, epochLength)
}
