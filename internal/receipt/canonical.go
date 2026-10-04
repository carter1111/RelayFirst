package receipt

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
)

// canonicalJSON serialises v with a deterministic byte-level representation:
//
//   - object keys sorted lexicographically (byte order)
//   - no insignificant whitespace
//   - integers rendered in decimal, without exponent or leading zeros
//   - nil pointers rendered as null
//
// This exists because the receipt's signing digest is keccak256 over the
// canonical bytes. If Go and TypeScript disagree by a single byte, signatures
// will not verify across implementations — which is exactly the failure mode
// the KAT vectors are designed to catch (CODING_RULES.md §4).
//
// Note: Go's encoding/json already sorts map keys, but it escapes HTML by
// default and formats some values differently from ECMAScript. Rather than rely
// on those incidental guarantees, we marshal explicitly.
func canonicalJSON(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
		return nil

	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil

	case string:
		writeJSONString(b, t)
		return nil

	case []byte:
		writeJSONString(b, "0x"+hex.EncodeToString(t))
		return nil

	case int:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case int8:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case int16:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case int32:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case int64:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case uint:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case uint8:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case uint16:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case uint32:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case uint64:
		b.WriteString(fmt.Sprintf("%d", t))
		return nil
	case *big.Int:
		if t == nil {
			b.WriteString("null")
			return nil
		}
		b.WriteString(t.String())
		return nil

	case []Anchor:
		return writeSlice(b, len(t), func(i int) any { return t[i] })
	case []string:
		return writeSlice(b, len(t), func(i int) any { return t[i] })
	case []any:
		return writeSlice(b, len(t), func(i int) any { return t[i] })

	case Anchor:
		return writeStruct(b, []kv{
			{"url", t.URL},
			{"contentHash", t.ContentHash},
			{"fetchedAt", t.FetchedAt},
			{"status", t.Status},
			{"bytes", t.Bytes},
		})

	case Task:
		return writeStruct(b, []kv{
			{"type", string(t.Type)},
			{"spec", t.Spec},
			{"specHash", t.SpecHash},
			{"selfGenerated", t.SelfGenerated},
			{"a2aTaskId", t.A2ATaskID},
		})

	case Work:
		return writeStruct(b, []kv{
			{"provider", t.Provider},
			{"model", t.Model},
			{"tokensIn", t.TokensIn},
			{"tokensOut", t.TokensOut},
			{"startedAt", t.StartedAt},
			{"finishedAt", t.FinishedAt},
		})

	case Result:
		return writeStruct(b, []kv{
			{"value", t.Value},
			{"hash", t.Hash},
		})

	case Verification:
		return writeStruct(b, []kv{
			{"status", string(t.Status)},
			{"verifierId", t.VerifierID},
			{"verifiedAt", t.VerifiedAt},
			{"stake", t.Stake},
			{"recomputedHash", t.RecomputedHash},
		})

	case Receipt:
		return writeStruct(b, []kv{
			{"schema", t.Schema},
			{"receiptId", t.ReceiptID},
			{"agentId", t.AgentID},
			{"epoch", t.Epoch},
			{"task", t.Task},
			{"work", t.Work},
			{"result", t.Result},
			{"anchors", t.Anchors},
			{"verification", t.Verification},
			{"signature", t.Signature},
		})

	case signedPayload:
		return writeStruct(b, []kv{
			{"agentId", t.AgentID},
			{"epoch", t.Epoch},
			{"task", t.Task},
			{"work", t.Work},
			{"result", t.Result},
			{"anchors", t.Anchors},
		})

	case map[string]any:
		return writeMap(b, t)
	}

	// Pointer and slice handling via reflection, so that optional fields
	// (*string, *int64) and any future slice element type work without the
	// writer needing a new switch case for each one.
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			b.WriteString("null")
			return nil
		}
		return writeCanonical(b, rv.Elem().Interface())

	case reflect.Slice, reflect.Array:
		b.WriteByte('[')
		for i := 0; i < rv.Len(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, rv.Index(i).Interface()); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	}

	return fmt.Errorf("canonicalJSON: unsupported type %T", v)
}

type kv struct {
	key string
	val any
}

// writeStruct writes an object with the keys in the order given. Struct field
// order is the schema's declared order, which is part of the wire format — it
// is deliberately not sorted so that the JSON reads in the same order as
// MVP.md §4.
func writeStruct(b *strings.Builder, fields []kv) error {
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(b, f.key)
		b.WriteByte(':')
		if err := writeCanonical(b, f.val); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// writeMap writes an object with sorted keys, for free-form maps such as
// task.spec whose field set is not known at compile time.
func writeMap(b *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(b, k)
		b.WriteByte(':')
		if err := writeCanonical(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func writeSlice(b *strings.Builder, n int, at func(int) any) error {
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := writeCanonical(b, at(i)); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}

// writeJSONString escapes s the way ECMAScript's JSON.stringify does, so the
// Go and TypeScript encoders agree on non-ASCII input.
//
// Differences from encoding/json: HTML characters are NOT escaped (Go escapes
// <, > and & by default; JSON.stringify does not), and control characters use
// \u00XX rather than Go's \xXX.
func writeJSONString(b *strings.Builder, s string) {
	const hexDigits = "0123456789abcdef"

	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r < 0x20:
				b.WriteString(`\u00`)
				b.WriteByte(hexDigits[(r>>4)&0xF])
				b.WriteByte(hexDigits[r&0xF])
			case r == 0x2028 || r == 0x2029:
				// JSON.stringify escapes these line separators.
				b.WriteString(`\u202`)
				if r == 0x2028 {
					b.WriteByte('8')
				} else {
					b.WriteByte('9')
				}
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// ------------------------------------------------------------- small helpers

func checkHash(field, h string) error {
	if strings.TrimSpace(h) == "" {
		return invalid("%s is empty", field)
	}
	raw, err := hexDecode(strings.TrimPrefix(h, "sha256:"))
	if err != nil {
		return invalid("%s is not valid hex: %v", field, err)
	}
	if len(raw) != 32 {
		return invalid("%s must be 32 bytes, got %d", field, len(raw))
	}
	return nil
}

func checkHexLen(field, s string, want int) error {
	raw, err := hexDecode(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return invalid("%s is not valid hex: %v", field, err)
	}
	if len(raw) != want {
		return invalid("%s must be %d bytes, got %d", field, want, len(raw))
	}
	return nil
}

func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }

func toHex(b []byte) string { return hex.EncodeToString(b) }

func itoa(i int) string { return fmt.Sprintf("%d", i) }

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
