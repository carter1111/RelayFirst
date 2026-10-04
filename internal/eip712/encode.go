// Package eip712 implements EIP-712 typed structured data hashing.
//
// Scope: this is the *encoding* layer only (encodeType / typeHash / encodeData /
// hashStruct / domain separator). It deliberately does NOT implement keccak256 —
// that comes from golang.org/x/crypto/sha3 (see CODING_RULES.md §1, invariant A1).
//
// The implementation follows https://eips.ethereum.org/EIPS/eip-712 and is
// validated against fixed KAT vectors shared with the TypeScript side (viem).
// See CODING_RULES.md §4 — the vectors are a hard CI gate.
package eip712

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Keccak256 returns the keccak-256 digest of the concatenation of data.
func Keccak256(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

// Field is one member of a struct type.
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Types maps a struct type name to its ordered fields. Field order is
// significant: encodeType and encodeData both follow the declared order.
type Types map[string][]Field

// Domain is the EIP-712 domain. Only non-empty fields are included in the
// EIP712Domain type, matching viem and apitypes behaviour.
//
// Note (ARCHITECTURE.md §3.6, MVP.md §8): offchain RelayFirst events use a
// chain-agnostic domain — no chainId, no verifyingContract — so that signing an
// offchain event never requires chain knowledge. Chain-bound domains are used
// only for onchain-anchored objects (delegations, settlements).
type Domain struct {
	Name              string   `json:"name,omitempty"`
	Version           string   `json:"version,omitempty"`
	ChainID           *big.Int `json:"chainId,omitempty"`
	VerifyingContract string   `json:"verifyingContract,omitempty"`
	Salt              string   `json:"salt,omitempty"` // 0x-prefixed bytes32
}

// TypedData is the JSON shape used by eth_signTypedData_v4, so the same vectors
// can be fed to viem, apitypes, or this implementation.
type TypedData struct {
	Types       Types          `json:"types"`
	PrimaryType string         `json:"primaryType"`
	Domain      Domain         `json:"domain"`
	Message     map[string]any `json:"message"`
}

// domainFields returns the ordered fields of EIP712Domain for this domain,
// omitting empty members. Field order follows the spec.
func (d Domain) domainFields() []Field {
	var out []Field
	if d.Name != "" {
		out = append(out, Field{Name: "name", Type: "string"})
	}
	if d.Version != "" {
		out = append(out, Field{Name: "version", Type: "string"})
	}
	if d.ChainID != nil {
		out = append(out, Field{Name: "chainId", Type: "uint256"})
	}
	if d.VerifyingContract != "" {
		out = append(out, Field{Name: "verifyingContract", Type: "address"})
	}
	if d.Salt != "" {
		out = append(out, Field{Name: "salt", Type: "bytes32"})
	}
	return out
}

func (d Domain) domainMessage() map[string]any {
	m := map[string]any{}
	if d.Name != "" {
		m["name"] = d.Name
	}
	if d.Version != "" {
		m["version"] = d.Version
	}
	if d.ChainID != nil {
		m["chainId"] = d.ChainID
	}
	if d.VerifyingContract != "" {
		m["verifyingContract"] = d.VerifyingContract
	}
	if d.Salt != "" {
		m["salt"] = d.Salt
	}
	return m
}

// EncodeType returns the EIP-712 encodeType string for primary:
// the primary struct followed by every referenced struct, sorted by name.
//
//	Mail(Person from,Person to,string contents)Person(string name,address wallet)
func EncodeType(types Types, primary string) (string, error) {
	if _, ok := types[primary]; !ok {
		return "", fmt.Errorf("eip712: unknown primary type %q", primary)
	}

	var b strings.Builder
	write := func(name string) error {
		fields, ok := types[name]
		if !ok {
			return fmt.Errorf("eip712: unknown struct type %q", name)
		}
		b.WriteString(name)
		b.WriteByte('(')
		for i, f := range fields {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(f.Type)
			b.WriteByte(' ')
			b.WriteString(f.Name)
		}
		b.WriteByte(')')
		return nil
	}

	if err := write(primary); err != nil {
		return "", err
	}
	for _, dep := range referencedStructs(types, primary) {
		if err := write(dep); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// referencedStructs returns every struct type reachable from typeName
// (excluding typeName itself), sorted alphabetically.
func referencedStructs(types Types, typeName string) []string {
	seen := map[string]bool{}
	var out []string

	var walk func(string)
	walk = func(name string) {
		for _, f := range types[name] {
			base := baseType(f.Type)
			if _, isStruct := types[base]; !isStruct {
				continue
			}
			if base == name || seen[base] {
				continue
			}
			seen[base] = true
			out = append(out, base)
			walk(base)
		}
	}
	walk(typeName)

	sortStrings(out)
	return out
}

// TypeHash returns keccak256(encodeType(primary)).
func TypeHash(types Types, primary string) ([]byte, error) {
	enc, err := EncodeType(types, primary)
	if err != nil {
		return nil, err
	}
	return Keccak256([]byte(enc)), nil
}

// HashStruct computes hashStruct(s) = keccak256(typeHash ‖ encodeData(s)).
func HashStruct(types Types, typeName string, data map[string]any) ([]byte, error) {
	th, err := TypeHash(types, typeName)
	if err != nil {
		return nil, err
	}
	enc, err := EncodeData(types, typeName, data)
	if err != nil {
		return nil, err
	}
	return Keccak256(th, enc), nil
}

// EncodeData returns encodeData(s): the concatenation of the 32-byte encodings
// of every member, in declared order.
func EncodeData(types Types, typeName string, data map[string]any) ([]byte, error) {
	fields, ok := types[typeName]
	if !ok {
		return nil, fmt.Errorf("eip712: unknown struct type %q", typeName)
	}

	out := make([]byte, 0, 32*len(fields))
	for _, f := range fields {
		v, present := data[f.Name]
		if !present {
			return nil, fmt.Errorf("eip712: missing field %q on %s", f.Name, typeName)
		}
		enc, err := encodeValue(types, f.Type, v)
		if err != nil {
			return nil, fmt.Errorf("eip712: field %s.%s (%s): %w", typeName, f.Name, f.Type, err)
		}
		out = append(out, enc...)
	}
	return out, nil
}

// DomainSeparator returns hashStruct of the EIP712Domain for d.
func DomainSeparator(d Domain) ([]byte, error) {
	fields := d.domainFields()
	if len(fields) == 0 {
		return nil, fmt.Errorf("eip712: domain has no fields")
	}
	types := Types{"EIP712Domain": fields}
	return HashStruct(types, "EIP712Domain", d.domainMessage())
}

// HashTypedData returns the final signing digest:
//
//	keccak256("\x19\x01" ‖ domainSeparator ‖ hashStruct(message))
func HashTypedData(td TypedData) ([]byte, error) {
	ds, err := DomainSeparator(td.Domain)
	if err != nil {
		return nil, err
	}
	ms, err := HashStruct(td.Types, td.PrimaryType, td.Message)
	if err != nil {
		return nil, err
	}
	return Keccak256([]byte{0x19, 0x01}, ds, ms), nil
}

// ---------------------------------------------------------------- internals

// baseType strips all array suffixes: "Person[]" -> "Person",
// "uint256[2]" -> "uint256".
func baseType(t string) string {
	if i := strings.IndexByte(t, '['); i >= 0 {
		return t[:i]
	}
	return t
}

// arrayDims returns the array suffixes of t, outermost first.
func arrayDims(t string) []string {
	var dims []string
	rest := t
	for {
		i := strings.IndexByte(rest, '[')
		if i < 0 {
			return dims
		}
		j := strings.IndexByte(rest[i:], ']')
		if j < 0 {
			return dims
		}
		dims = append(dims, rest[i:i+j+1])
		rest = rest[i+j+1:]
	}
}

// encodeValue produces the 32-byte EIP-712 encoding of v of the given type.
func encodeValue(types Types, typ string, v any) ([]byte, error) {
	if dims := arrayDims(typ); len(dims) > 0 {
		items, err := asSlice(v)
		if err != nil {
			return nil, err
		}
		elem := baseType(typ)

		// Fixed-size arrays must match their declared length; variable
		// arrays are length-agnostic. Both encode as keccak256(concat).
		if inner := dims[0]; inner != "[]" {
			want, err := arrayLen(inner)
			if err != nil {
				return nil, err
			}
			if len(items) != want {
				return nil, fmt.Errorf("fixed array %s expects %d elements, got %d", typ, want, len(items))
			}
		}

		if len(dims) > 1 {
			// Multidimensional: recurse on the remaining dimensions.
			elem = elem + strings.Join(dims[1:], "")
		}

		buf := make([]byte, 0, 32*len(items))
		for i, item := range items {
			enc, err := encodeValue(types, elem, item)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			buf = append(buf, enc...)
		}
		return Keccak256(buf), nil
	}

	if _, isStruct := types[typ]; isStruct {
		m, err := asMap(v)
		if err != nil {
			return nil, err
		}
		return HashStruct(types, typ, m)
	}

	switch {
	case typ == "string":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", v)
		}
		return Keccak256([]byte(s)), nil

	case typ == "bytes":
		b, err := asBytes(v)
		if err != nil {
			return nil, err
		}
		return Keccak256(b), nil

	case typ == "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", v)
		}
		if b {
			return leftPad32([]byte{1}), nil
		}
		return leftPad32([]byte{0}), nil

	case typ == "address":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected address string, got %T", v)
		}
		raw, err := hexStrip(s)
		if err != nil {
			return nil, err
		}
		if len(raw) != 20 {
			return nil, fmt.Errorf("address must be 20 bytes, got %d", len(raw))
		}
		return leftPad32(raw), nil

	case strings.HasPrefix(typ, "bytes"):
		// Fixed-size bytesN: right-padded to 32.
		n, err := bytesLen(typ)
		if err != nil {
			return nil, err
		}
		b, err := asBytes(v)
		if err != nil {
			return nil, err
		}
		if len(b) != n {
			return nil, fmt.Errorf("%s expects %d bytes, got %d", typ, n, len(b))
		}
		out := make([]byte, 32)
		copy(out, b)
		return out, nil

	case strings.HasPrefix(typ, "uint"):
		n, err := intBits(typ, "uint")
		if err != nil {
			return nil, err
		}
		bi, err := asBigInt(v)
		if err != nil {
			return nil, err
		}
		if bi.Sign() < 0 {
			return nil, fmt.Errorf("%s cannot encode negative value", typ)
		}
		if bi.BitLen() > n {
			return nil, fmt.Errorf("%s overflow: value needs %d bits", typ, bi.BitLen())
		}
		return leftPad32(bi.Bytes()), nil

	case strings.HasPrefix(typ, "int"):
		n, err := intBits(typ, "int")
		if err != nil {
			return nil, err
		}
		bi, err := asBigInt(v)
		if err != nil {
			return nil, err
		}
		return twosComplement32(bi, n)
	}

	return nil, fmt.Errorf("unsupported EIP-712 type %q", typ)
}

// leftPad32 left-pads b with zero bytes to exactly 32 bytes.
func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// twosComplement32 returns the 32-byte big-endian two's complement encoding of
// v, which must fit in bits (including sign).
func twosComplement32(v *big.Int, bits int) ([]byte, error) {
	if v.Sign() >= 0 {
		if v.BitLen() > bits-1 {
			return nil, fmt.Errorf("int%d overflow: value needs %d bits", bits, v.BitLen()+1)
		}
		return leftPad32(v.Bytes()), nil
	}

	// Negative: encode as 2^256 + v, then take the low 32 bytes.
	mod := new(big.Int).Lsh(big.NewInt(1), 256)
	enc := new(big.Int).Add(mod, v)
	if enc.BitLen() > 256 {
		return nil, fmt.Errorf("int%d underflow", bits)
	}
	b := enc.Bytes()
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out, nil
}

func arrayLen(suffix string) (int, error) {
	if len(suffix) < 2 || suffix[0] != '[' || suffix[len(suffix)-1] != ']' {
		return 0, fmt.Errorf("malformed array suffix %q", suffix)
	}
	inner := suffix[1 : len(suffix)-1]
	if inner == "" {
		return 0, fmt.Errorf("not a fixed-size array")
	}
	n, err := asBigInt(inner)
	if err != nil {
		return 0, fmt.Errorf("malformed array length %q", inner)
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("array length too large")
	}
	return int(n.Int64()), nil
}

func bytesLen(typ string) (int, error) {
	n, err := asBigInt(typ[len("bytes"):])
	if err != nil {
		return 0, fmt.Errorf("malformed %q", typ)
	}
	if !n.IsInt64() || n.Int64() < 1 || n.Int64() > 32 {
		return 0, fmt.Errorf("bytesN must be 1..32, got %q", typ)
	}
	return int(n.Int64()), nil
}

func intBits(typ, prefix string) (int, error) {
	rest := typ[len(prefix):]
	if rest == "" {
		return 256, nil // bare uint/int default to 256
	}
	n, err := asBigInt(rest)
	if err != nil {
		return 0, fmt.Errorf("malformed %q", typ)
	}
	if !n.IsInt64() || n.Int64() <= 0 || n.Int64() > 256 || n.Int64()%8 != 0 {
		return 0, fmt.Errorf("%s must be a multiple of 8 in 8..256, got %q", prefix, typ)
	}
	return int(n.Int64()), nil
}

func hexStrip(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex")
	}
	return hex.DecodeString(s)
}

func asBytes(v any) ([]byte, error) {
	switch t := v.(type) {
	case string:
		return hexStrip(t)
	case []byte:
		return t, nil
	}
	return nil, fmt.Errorf("expected hex string or []byte, got %T", v)
}

func asMap(v any) (map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("expected object, got %T", v)
}

func asSlice(v any) ([]any, error) {
	if s, ok := v.([]any); ok {
		return s, nil
	}
	return nil, fmt.Errorf("expected array, got %T", v)
}

func asBigInt(v any) (*big.Int, error) {
	switch t := v.(type) {
	case *big.Int:
		return t, nil
	case string:
		s := strings.TrimSpace(t)
		neg := strings.HasPrefix(s, "-")
		body := strings.TrimPrefix(s, "-")

		var bi *big.Int
		var ok bool
		if strings.HasPrefix(body, "0x") || strings.HasPrefix(body, "0X") {
			bi, ok = new(big.Int).SetString(body[2:], 16)
		} else {
			// Bare strings are decimal. Parsing them as hex would silently
			// reinterpret "250" as 0x250 = 592 — a correctness bug that is
			// invisible until a hash mismatch appears.
			bi, ok = new(big.Int).SetString(body, 10)
		}
		if !ok {
			return nil, fmt.Errorf("cannot parse integer %q", t)
		}
		if neg {
			bi.Neg(bi)
		}
		return bi, nil
	case int:
		return big.NewInt(int64(t)), nil
	case int64:
		return big.NewInt(t), nil
	case uint64:
		return new(big.Int).SetUint64(t), nil
	}
	return nil, fmt.Errorf("expected integer, got %T", v)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
