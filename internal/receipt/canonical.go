package receipt

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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

	// Numbers decoded from JSON (H1, S9-0i).
	//
	// Task.Spec is map[string]any, so any non-string JSON value decodes to
	// float64. Without these cases the type switch fell through to reflection and
	// errored, which meant a spec containing a number either crashed export or
	// made a valid signed receipt fail verification.
	//
	// The rendering must match encoding/json byte for byte, because the structural
	// path and the verbatim-payload path have to agree: if they rendered 5
	// differently, checkPayloadConsistency would reject an honest receipt. See
	// formatJSONFloat, which mirrors encoding/json's rules exactly.
	case float64:
		s, err := formatJSONFloat(t)
		if err != nil {
			return err
		}
		b.WriteString(s)
		return nil

	case float32:
		s, err := formatJSONFloat(float64(t))
		if err != nil {
			return err
		}
		b.WriteString(s)
		return nil

	case json.Number:
		// json.Number carries the original literal, so emit it verbatim — but
		// validate it first, or a caller could smuggle arbitrary bytes into the
		// canonical form and change a signed payload.
		s := t.String()
		if !isValidJSONNumber(s) {
			return fmt.Errorf("canonicalJSON: json.Number %q is not a valid JSON number", s)
		}
		b.WriteString(s)
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
		fields := []kv{
			{"type", string(t.Type)},
			{"spec", t.Spec},
			{"specHash", t.SpecHash},
			{"selfGenerated", t.SelfGenerated},
			{"a2aTaskId", t.A2ATaskID},
		}
		// verification is appended ONLY when set, matching the struct tag's
		// omitempty.
		//
		// This condition is not cosmetic. Every receipt signed before the field
		// existed has no verification key in its canonical bytes, and those bytes
		// are what its payload hash covers. Emitting the key unconditionally — even
		// as an empty string — would change the bytes of every historical receipt,
		// changing its hash and invalidating every signature ever made. The
		// conditional is the difference between a minor change and destroying the
		// corpus.
		if t.Verification != "" {
			fields = append(fields, kv{"verification", string(t.Verification)})
		}
		return writeStruct(b, fields)

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
		// The order here must match the struct declaration in receipt.go. The
		// final `payload` key is what carries the verbatim signed bytes through
		// persistence, export and relay (S9-0b2).
		//
		// It was originally omitted, which silently destroyed the signed blob on
		// every canonical path — store, export and publish — so a receipt signed
		// with a verbatim payload came back as a structural one. The bug survived
		// a passing test because that test asserted on json.Marshal while this
		// writer is what the real paths use. Any test for this must go through
		// MarshalCanonical.
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
			{"payload", t.Payload},
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

// formatJSONFloat renders a float64 exactly as encoding/json does.
//
// # Why byte-exactness matters here
//
// The structural path and the verbatim-payload path must agree. If one rendered
// 5.0 as "5" and the other as "5.0", checkPayloadConsistency would reject an
// honest receipt — and worse, a Go/TypeScript disagreement would break
// cross-language verification (invariant A4).
//
// # Why not strconv.FormatFloat alone
//
// encoding/json has its own rules: it prefers the shortest representation that
// round-trips, switches to exponent form outside a specific magnitude range, and
// normalises the exponent (e+07 becomes e+7). Reimplementing those rules by hand
// is how the two sides drift. Instead this delegates to encoding/json for the
// number itself and only rejects the cases JSON cannot represent, so the output
// is byte-identical by construction rather than by agreement.
func formatJSONFloat(f float64) (string, error) {
	// NaN and infinities are not valid JSON. encoding/json rejects them, and so
	// must this, or a receipt could carry a value that no other implementation
	// can parse.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("canonicalJSON: %v is not representable in JSON", f)
	}

	out, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("canonicalJSON: marshal float: %w", err)
	}
	return string(out), nil
}

// isValidJSONNumber reports whether s is a well-formed JSON number.
//
// It exists because json.Number is a string type: a caller could construct one
// holding arbitrary bytes, and emitting that verbatim would let unvalidated
// content into the canonical form — which is the bytes a signature covers.
func isValidJSONNumber(s string) bool {
	if s == "" {
		return false
	}
	// The grammar is small enough to check directly, and doing so avoids a
	// round-trip through encoding/json just to reject obvious garbage.
	i, n := 0, len(s)

	if s[i] == '-' {
		i++
	}
	if i >= n {
		return false
	}

	// int: 0 | [1-9][0-9]*
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}

	// frac: .[0-9]+
	if i < n && s[i] == '.' {
		i++
		start := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}

	// exp: [eE][+-]?[0-9]+
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < n && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}

	return i == n
}
