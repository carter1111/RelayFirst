package eip712

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// VectorFile is the shared cross-language KAT corpus. viem generates it;
// this test must reproduce every digest byte-for-byte.
const vectorFile = "../../testdata/eip712-vectors.json"

type vectorCorpus struct {
	Reference string   `json:"reference"`
	Count     int      `json:"count"`
	Vectors   []vector `json:"vectors"`
}

type vector struct {
	Name            string    `json:"name"`
	Note            string    `json:"note"`
	TypedData       TypedData `json:"typedData"`
	DomainSeparator string    `json:"domainSeparator"`
	MessageHash     string    `json:"messageHash"`
	Digest          string    `json:"digest"`
}

func loadVectors(t *testing.T) vectorCorpus {
	t.Helper()

	path, err := filepath.Abs(vectorFile)
	if err != nil {
		t.Fatalf("resolve vector path: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run `npm run gen:kat` first): %v", path, err)
	}

	// Integers arrive either as decimal strings or as JSON numbers; the corpus
	// uses strings, but json.Number keeps bare numbers exact if that changes.
	dec := json.NewDecoder(newReader(raw))
	dec.UseNumber()

	var corpus vectorCorpus
	if err := dec.Decode(&corpus); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if corpus.Count != len(corpus.Vectors) {
		t.Fatalf("corpus count %d does not match %d vectors", corpus.Count, len(corpus.Vectors))
	}
	if corpus.Count == 0 {
		t.Fatal("corpus is empty")
	}
	return corpus
}

// TestKAT_Vectors is the hard cross-language gate from CODING_RULES.md §4.
// A failure here means the Go implementation and viem disagree, which would
// make signatures non-portable across the Go and TypeScript SDKs.
func TestKAT_Vectors(t *testing.T) {
	corpus := loadVectors(t)
	t.Logf("reference implementation: %s, %d vectors", corpus.Reference, corpus.Count)

	for _, v := range corpus.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			// Stage 1: domain separator. Checked separately so a mismatch
			// localises to the domain rather than the message.
			gotDomain, err := DomainSeparator(v.TypedData.Domain)
			if err != nil {
				t.Fatalf("DomainSeparator: %v", err)
			}
			if h := hexOf(gotDomain); h != v.DomainSeparator {
				t.Errorf("domain separator mismatch\n  got  %s\n  want %s", h, v.DomainSeparator)
			}

			// Stage 2: hashStruct(message).
			msg, err := normalise(v.TypedData.Message)
			if err != nil {
				t.Fatalf("normalise message: %v", err)
			}
			gotMsg, err := HashStruct(v.TypedData.Types, v.TypedData.PrimaryType, msg)
			if err != nil {
				t.Fatalf("HashStruct: %v", err)
			}
			if h := hexOf(gotMsg); h != v.MessageHash {
				t.Errorf("message hash mismatch\n  got  %s\n  want %s", h, v.MessageHash)
			}

			// Stage 3: the final signing digest.
			td := v.TypedData
			td.Message = msg
			gotDigest, err := HashTypedData(td)
			if err != nil {
				t.Fatalf("HashTypedData: %v", err)
			}
			if h := hexOf(gotDigest); h != v.Digest {
				t.Errorf("digest mismatch\n  got  %s\n  want %s", h, v.Digest)
			}
		})
	}
}

// TestEncodeType locks the type-string construction, which is the root of
// every typeHash. Nested structs must appear sorted after the primary type.
func TestEncodeType(t *testing.T) {
	types := Types{
		"Outer": {
			{Name: "id", Type: "bytes32"},
			{Name: "inner", Type: "Inner"},
		},
		"Inner": {
			{Name: "label", Type: "string"},
			{Name: "amount", Type: "uint256"},
		},
	}

	got, err := EncodeType(types, "Outer")
	if err != nil {
		t.Fatalf("EncodeType: %v", err)
	}
	want := "Outer(bytes32 id,Inner inner)Inner(string label,uint256 amount)"
	if got != want {
		t.Errorf("encodeType mismatch\n  got  %s\n  want %s", got, want)
	}
}

// TestEncodeType_SkipsSelfReference guards against infinite recursion when a
// struct refers to itself (e.g. a linked-list style schema).
func TestEncodeType_SkipsSelfReference(t *testing.T) {
	types := Types{
		"Node": {
			{Name: "value", Type: "uint256"},
			{Name: "next", Type: "Node"},
		},
	}

	got, err := EncodeType(types, "Node")
	if err != nil {
		t.Fatalf("EncodeType: %v", err)
	}
	want := "Node(uint256 value,Node next)"
	if got != want {
		t.Errorf("self-referential encodeType mismatch\n  got  %s\n  want %s", got, want)
	}
}
