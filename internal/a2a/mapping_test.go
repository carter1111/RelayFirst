package a2a

import "testing"

// TestSchemaRelation_CoversSchemaVersion keeps the table and the pinned version
// from drifting: the version we say we target must be a version we have a write
// mapping for.
func TestSchemaRelation_CoversSchemaVersion(t *testing.T) {
	majors, err := WritableSchemaMajors(SchemaVersion)
	if err != nil {
		t.Fatalf("the A2A version we target (%s) has no schema mapping: %v", SchemaVersion, err)
	}
	if len(majors) == 0 {
		t.Fatalf("%s maps to no writable schema majors", SchemaVersion)
	}
}

func TestWritableSchemaMajors(t *testing.T) {
	majors, err := WritableSchemaMajors("1.0")
	if err != nil {
		t.Fatalf("1.0: %v", err)
	}
	if len(majors) == 0 {
		t.Fatal("1.0 must map to at least one writable schema major")
	}
	// Newest first, so a caller that takes [0] gets the preferred version.
	for i := 1; i < len(majors); i++ {
		if majors[i-1] < majors[i] {
			t.Errorf("majors must be newest first, got %v", majors)
		}
	}
}

// TestWritableSchemaMajors_UnknownVersionIsAnError guards the failure mode where
// a missing entry looks like "no writes allowed" instead of "you forgot to map
// this version".
func TestWritableSchemaMajors_UnknownVersionIsAnError(t *testing.T) {
	if _, err := WritableSchemaMajors("9.9"); err == nil {
		t.Fatal("an unmapped A2A version must be an error, not an empty allow-list")
	}
}

// TestWritableSchemaMajors_MalformedIsAParseError keeps malformed input distinct
// from an unmapped-but-valid version, for the same reason negotiation does.
func TestWritableSchemaMajors_MalformedIsAParseError(t *testing.T) {
	if _, err := WritableSchemaMajors("not-a-version"); err == nil {
		t.Fatal("malformed version must fail")
	}
}

// TestWritableSchemaMajors_ReturnsACopy makes sure a caller cannot mutate the
// package-level table through the returned slice.
func TestWritableSchemaMajors_ReturnsACopy(t *testing.T) {
	got, err := WritableSchemaMajors("1.0")
	if err != nil {
		t.Fatalf("1.0: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("need a non-empty result to test aliasing")
	}
	original := SchemaRelation["1.0"][0]
	got[0] = 987654
	if SchemaRelation["1.0"][0] != original {
		t.Fatal("WritableSchemaMajors leaked the package table; a caller mutated global state")
	}
}
