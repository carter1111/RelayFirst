package receipt

import (
	"encoding/json"
	"strings"
	"testing"
)

// These tests cover S9-10: linking a receipt to the A2A task it was produced for.

// TestA2ATaskID_NullIsAlwaysSerialized pins the canonical writer's behaviour for this
// key, and carries a correction worth reading before changing it.
//
// The first version of this test (and of the field's doc comment) claimed that adding
// `omitempty` to the struct tag would strip the key from historical receipts and
// invalidate every signature. A mutation test disproved it: the signed bytes come from
// the explicit field list in canonical.go, not from struct tags, so the tag edit left
// the frozen corpus intact. The dangerous edit is removing the field from
// canonical.go, and that mutation does fail — see
// TestA2ATaskID_CanonicalWriterEmitsTheKey.
//
// The distinction matters because a reader who believed the original claim would guard
// the wrong line. It is recorded here rather than quietly fixed.
func TestA2ATaskID_NullIsAlwaysSerialized(t *testing.T) {
	r := validReceipt(t)
	if r.Task.A2ATaskID != nil {
		t.Fatal("the fixture must leave the link unset for this test to mean anything")
	}

	var doc struct {
		Task map[string]any `json:"task"`
	}
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	val, present := doc.Task["a2aTaskId"]
	if !present {
		t.Fatal("a2aTaskId must always be present in the canonical bytes, even when null; " +
			"adding omitempty would strip the key from every historical receipt and " +
			"invalidate every signature ever made")
	}
	if val != nil {
		t.Errorf("an unset link must serialize as null, got %v", val)
	}
}

// TestA2ATaskID_IsInsideTheSignature is the integrity property: an intermediary must
// not be able to retarget a receipt at a different A2A task.
func TestA2ATaskID_IsInsideTheSignature(t *testing.T) {
	r := validReceipt(t)
	if err := r.Task.SetA2ATaskID("tsk_original"); err != nil {
		t.Fatalf("set: %v", err)
	}
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("a linked receipt must validate: %v", err)
	}

	// Retarget the link AND keep the id consistent, so only the signature stands
	// between the attacker and a rewritten provenance.
	other := "tsk_someone_elses_task"
	r.Task.A2ATaskID = &other
	id, err = r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id

	err = r.Validate(nil)
	if err == nil {
		t.Fatal("retargeting the A2A task link must be detected; " +
			"otherwise an intermediary could redirect a receipt's provenance")
	}
	if !strings.Contains(err.Error(), "signer mismatch") {
		t.Errorf("the link is inside the signed payload, so tampering must fail signature recovery, got: %v", err)
	}
}

// TestA2ATaskID_SetRoundTrips distinguishes "no task" from "a task with an empty id".
func TestA2ATaskID_SetRoundTrips(t *testing.T) {
	r := validReceipt(t)
	if err := r.Task.SetA2ATaskID("tsk_abc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	back, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Task.A2ATaskID == nil {
		t.Fatal("a set link must survive a round trip")
	}
	if *back.Task.A2ATaskID != "tsk_abc" {
		t.Errorf("link = %q, want %q", *back.Task.A2ATaskID, "tsk_abc")
	}

	// And clearing it restores null, not an empty string.
	back.Task.ClearA2ATaskID()
	if back.Task.A2ATaskID != nil {
		t.Error("clearing the link must set nil, not an empty string")
	}
}

func TestSetA2ATaskID_RejectsEmpty(t *testing.T) {
	var task Task
	for _, bad := range []string{"", "   ", "\t\n"} {
		if err := task.SetA2ATaskID(bad); err == nil {
			t.Errorf("SetA2ATaskID(%q) must fail: an empty link is a meaningless link", bad)
		}
	}
	if task.A2ATaskID != nil {
		t.Error("a rejected set must not have mutated the task")
	}
}

func TestSetA2ATaskID_RejectsOverlong(t *testing.T) {
	var task Task
	long := strings.Repeat("x", MaxA2ATaskIDLength+1)
	if err := task.SetA2ATaskID(long); err == nil {
		t.Error("an overlong id must be rejected so it cannot be used as unbounded storage")
	}

	// Exactly at the limit must pass, or the bound is off by one.
	atLimit := strings.Repeat("x", MaxA2ATaskIDLength)
	if err := task.SetA2ATaskID(atLimit); err != nil {
		t.Errorf("an id of exactly %d bytes must be accepted: %v", MaxA2ATaskIDLength, err)
	}
}

// TestValidateA2ATaskID_AcceptsOpaqueForms is the interoperability requirement.
//
// A2A does not specify a task id grammar — an agent chooses its own. A strict check
// would reject a newer peer's ids for no reason, which is the same mistake as
// demanding an exact schema match.
func TestValidateA2ATaskID_AcceptsOpaqueForms(t *testing.T) {
	ok := []string{
		"tsk_01H8XYZ",
		"a2a-task-42",
		"https://agent.example/tasks/9",
		"01J8XYZ-0001",
		strings.Repeat("x", MaxA2ATaskIDLength),
		"任务-42", // a non-ASCII id is still an id
	}
	for _, id := range ok {
		if err := ValidateA2ATaskID(id); err != nil {
			t.Errorf("ValidateA2ATaskID(%q) must accept an opaque id: %v", id, err)
		}
	}
}

func TestValidateA2ATaskID_RejectsEmptyAndOverlong(t *testing.T) {
	bad := []string{"", "  ", strings.Repeat("x", MaxA2ATaskIDLength+1)}
	for _, id := range bad {
		if err := ValidateA2ATaskID(id); err == nil {
			t.Errorf("ValidateA2ATaskID(%q) must fail", id)
		}
	}
}

// TestValidate_RejectsEmptyA2ATaskIDInReceipt is the structural check, reached
// through Validate rather than the helper, so a hand-built receipt cannot skip it.
func TestValidate_RejectsEmptyA2ATaskIDInReceipt(t *testing.T) {
	r := validReceipt(t)
	empty := ""
	r.Task.A2ATaskID = &empty

	if err := r.Validate(nil); err == nil {
		t.Fatal("a receipt carrying an empty a2aTaskId must be rejected: " +
			"the caller meant null, and an empty string claims a link that cannot be followed")
	}
}

// TestValidate_AcceptsNilA2ATaskID is the ordinary case: self-generated work.
func TestValidate_AcceptsNilA2ATaskID(t *testing.T) {
	r := validReceipt(t)
	r.Task.A2ATaskID = nil
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("self-generated work has no A2A task and must validate: %v", err)
	}
}

// TestA2ATaskID_CanonicalWriterEmitsTheKey is the test the mutation pointed at.
//
// This is the line that actually protects historical receipts: canonical.go's Task case
// emits the key explicitly, and deleting it there fails the frozen corpus. The struct tag
// is pinned only so a direct encoding/json path cannot disagree with the signed form.
func TestA2ATaskID_CanonicalWriterEmitsTheKey(t *testing.T) {
	// A Task with no link must still render the key, because that is what historical
	// receipts contain.
	raw, err := canonicalJSON(Task{Type: TaskProbe, SpecHash: sha32("aa")})
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if !strings.Contains(string(raw), `"a2aTaskId":null`) {
		t.Errorf("the canonical writer must emit a2aTaskId even when unset, got: %s", raw)
	}

	// And a set link must render its value.
	linked := Task{Type: TaskProbe, SpecHash: sha32("aa")}
	if err := linked.SetA2ATaskID("tsk_canonical"); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err = canonicalJSON(linked)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if !strings.Contains(string(raw), `"a2aTaskId":"tsk_canonical"`) {
		t.Errorf("a set link must render its value, got: %s", raw)
	}
}

// TestA2ATaskID_StructTagMatchesTheWriter guards the secondary risk: if the tag and the
// canonical writer disagreed, a direct encoding/json path (an export, a debug dump) would
// produce different bytes than the signed form, and the difference would only be found by
// a human comparing two outputs.
func TestA2ATaskID_StructTagMatchesTheWriter(t *testing.T) {
	r := validReceipt(t)

	std, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var doc struct {
		Task map[string]any `json:"task"`
	}
	if err := json.Unmarshal(std, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := doc.Task["a2aTaskId"]; !present {
		t.Error("encoding/json must emit the key too, or an export would disagree with the signed form")
	}
}

// TestA2ATaskID_ReceiptSerializationIsUnchanged confirms S9-10 changed no historical bytes.
//
// The field already existed and already serialized; S9-10 only added validation. If
// this failed, the change would have been a wire change rather than a check.
func TestA2ATaskID_ReceiptSerializationIsUnchanged(t *testing.T) {
	r := validReceipt(t)
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The key must be present with a null value — the exact bytes a pre-S9-10 build
	// produced, since the pointer field always serialized.
	if !strings.Contains(string(raw), `"a2aTaskId":null`) {
		t.Errorf("the canonical form must be unchanged by S9-10; got: %s", raw)
	}
}
