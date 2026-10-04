/**
 * Generates the cross-language EIP-712 KAT vectors.
 *
 * CODING_RULES.md §4 makes these vectors a hard CI gate: viem (TypeScript) is
 * the reference implementation, and the Go side must produce byte-identical
 * hashes for every case. Without this, cross-language signatures diverge
 * silently and only surface months later.
 *
 * Usage:  npm run gen:kat
 * Output: testdata/eip712-vectors.json
 *
 * Coverage is deliberately chosen to hit the places where implementations
 * actually diverge: nested structs, dynamic arrays, long strings, empty values,
 * multibyte UTF-8, signed integers and bytesN padding.
 */
import { writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { hashTypedData, hashStruct, hashDomain, type TypedData } from 'viem'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')

/** A single known-answer case. */
interface Vector {
  name: string
  note: string
  typedData: TypedData
  domainSeparator: string
  messageHash: string
  digest: string
}

const vectors: Vector[] = []

function add(name: string, note: string, typedData: TypedData) {
  // viem exposes the intermediate hashes directly, which lets a mismatch point
  // at the exact failing stage rather than just "the digest differs".
  const domainSeparator = hashDomain({
    domain: typedData.domain,
    types: { EIP712Domain: domainTypeOf(typedData) },
  } as never)

  const messageHash = hashStruct({
    data: typedData.message,
    primaryType: typedData.primaryType,
    types: typedData.types,
  } as never)

  const digest = hashTypedData(typedData as never)

  vectors.push({ name, note, typedData: typedData as TypedData, domainSeparator, messageHash, digest })
}

function domainTypeOf(td: TypedData): Array<{ name: string; type: string }> {
  const d = td.domain as Record<string, unknown>
  const out: Array<{ name: string; type: string }> = []
  if (d.name !== undefined) out.push({ name: 'name', type: 'string' })
  if (d.version !== undefined) out.push({ name: 'version', type: 'string' })
  if (d.chainId !== undefined) out.push({ name: 'chainId', type: 'uint256' })
  if (d.verifyingContract !== undefined)
    out.push({ name: 'verifyingContract', type: 'address' })
  if (d.salt !== undefined) out.push({ name: 'salt', type: 'bytes32' })
  return out
}

/** The chain-agnostic domain every offchain RelayFirst event uses. */
const relayDomain = { name: 'RelayFirst', version: '1' } as const

const ADDR_A = '0x7F4dB0D9C4B8A6BCE8E1C22dA9c419E6e1F3A8B5' as const
const ADDR_B = '0x1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b' as const

// 1. Minimal single-field struct.
add('minimal', 'single uint256 field — the simplest possible case', {
  domain: relayDomain,
  types: { Only: [{ name: 'value', type: 'uint256' }] },
  primaryType: 'Only',
  message: { value: 1n },
})

// 2. Epoch as uint256 (the shape RelayReceipt actually uses).
add('relay-receipt-domain', 'chain-agnostic RelayFirst domain', {
  domain: relayDomain,
  types: {
    RelayReceipt: [
      { name: 'agentId', type: 'string' },
      { name: 'epoch', type: 'uint256' },
      { name: 'payloadHash', type: 'bytes32' },
    ],
  },
  primaryType: 'RelayReceipt',
  message: {
    agentId: `agent:eip155:8453:${ADDR_A}`,
    epoch: 42n,
    payloadHash:
      '0x3d4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f',
  },
})

// 3. Nested struct — the classic divergence point.
add('nested-struct', 'nested struct must include the inner type in encodeType', {
  domain: relayDomain,
  types: {
    Inner: [
      { name: 'label', type: 'string' },
      { name: 'amount', type: 'uint256' },
    ],
    Outer: [
      { name: 'id', type: 'bytes32' },
      { name: 'inner', type: 'Inner' },
      { name: 'flag', type: 'bool' },
    ],
  },
  primaryType: 'Outer',
  message: {
    id: '0x' + 'ab'.repeat(32),
    inner: { label: 'probe', amount: 1000000n },
    flag: true,
  },
})

// 4. Dynamic array of primitives.
add('dynamic-array-uint256', 'dynamic arrays hash as keccak256 of concatenated encodings', {
  domain: relayDomain,
  types: {
    WithArray: [
      { name: 'name', type: 'string' },
      { name: 'values', type: 'uint256[]' },
    ],
  },
  primaryType: 'WithArray',
  message: { name: 'trusted-urls', values: [1n, 2n, 3n, 250n] },
})

// 5. Dynamic array of structs.
add('dynamic-array-struct', 'array of structs — recursion plus array hashing', {
  domain: relayDomain,
  types: {
    Anchor: [
      { name: 'url', type: 'string' },
      { name: 'contentHash', type: 'bytes32' },
      { name: 'status', type: 'uint256' },
    ],
    Receipt: [
      { name: 'agentId', type: 'string' },
      { name: 'anchors', type: 'Anchor[]' },
    ],
  },
  primaryType: 'Receipt',
  message: {
    agentId: `agent:eip155:8453:${ADDR_A}`,
    anchors: [
      {
        url: 'https://api.example.com/health',
        contentHash: '0x' + '11'.repeat(32),
        status: 200n,
      },
      {
        url: 'https://api.example.com/data',
        contentHash: '0x' + '22'.repeat(32),
        status: 200n,
      },
    ],
  },
})

// 6. Fixed-size array.
add('fixed-array', 'fixed-size arrays enforce their declared length', {
  domain: relayDomain,
  types: {
    Fixed: [
      { name: 'pair', type: 'uint256[2]' },
      { name: 'word', type: 'bytes32[2]' },
    ],
  },
  primaryType: 'Fixed',
  message: {
    pair: [7n, 9n],
    word: ['0x' + 'aa'.repeat(32), '0x' + 'bb'.repeat(32)],
  },
})

// 7. Long string — exercises the string-hashing path across many blocks.
add('long-string', 'long strings must hash identically regardless of chunking', {
  domain: relayDomain,
  types: {
    LongText: [
      { name: 'subject', type: 'string' },
      { name: 'body', type: 'string' },
    ],
  },
  primaryType: 'LongText',
  message: {
    subject: 'proof of agent work',
    body: 'The quick brown fox jumps over the lazy dog. '.repeat(40),
  },
})

// 8. Empty values — the most common source of off-by-one bugs.
add('empty-values', 'empty string, empty array and zero integer', {
  domain: relayDomain,
  types: {
    Empties: [
      { name: 'emptyString', type: 'string' },
      { name: 'emptyArray', type: 'uint256[]' },
      { name: 'zero', type: 'uint256' },
      { name: 'emptyBytes', type: 'bytes' },
    ],
  },
  primaryType: 'Empties',
  message: {
    emptyString: '',
    emptyArray: [],
    zero: 0n,
    emptyBytes: '0x',
  },
})

// 9. Multibyte UTF-8 — catches byte-vs-rune length mistakes.
add('multibyte-utf8', 'non-ASCII must be hashed as UTF-8 bytes', {
  domain: relayDomain,
  types: {
    I18n: [
      { name: 'zh', type: 'string' },
      { name: 'emoji', type: 'string' },
      { name: 'mixed', type: 'string' },
    ],
  },
  primaryType: 'I18n',
  message: {
    zh: '立即中继。持续验证。仅当涉及价值时结算。',
    emoji: '🚀🌏🔐',
    mixed: 'agent:eip155:8453:0x7F4d — 头矿',
  },
})

// 10. Signed integers and address — negative two's complement plus address padding.
add('signed-int-and-address', 'negative int256 two\u0027s complement and address left-padding', {
  domain: relayDomain,
  types: {
    Mixed: [
      { name: 'neg', type: 'int256' },
      { name: 'pos', type: 'int256' },
      { name: 'beneficiary', type: 'address' },
      { name: 'hashes', type: 'bytes32[]' },
    ],
  },
  primaryType: 'Mixed',
  message: {
    neg: -12345n,
    pos: 67890n,
    beneficiary: ADDR_B,
    hashes: ['0x' + 'cc'.repeat(32), '0x' + 'dd'.repeat(32), '0x' + 'ee'.repeat(32)],
  },
})

const out = {
  $comment:
    'Generated by scripts/gen-kat.ts using viem. Do not edit by hand. ' +
    'CODING_RULES.md §4: the Go implementation must reproduce every digest byte-for-byte. ' +
    'Integer values are decimal strings so they survive JSON without precision loss.',
  reference: 'viem',
  count: vectors.length,
  vectors,
}

// JSON.stringify cannot serialise BigInt, so integers are emitted as decimal
// strings. The Go decoder accepts both decimal strings and numeric literals.
function bump(_key: string, value: unknown) {
  return typeof value === 'bigint' ? value.toString() : value
}

const target = join(root, 'testdata', 'eip712-vectors.json')
mkdirSync(dirname(target), { recursive: true })
writeFileSync(target, JSON.stringify(out, bump, 2) + '\n')

console.log(`wrote ${vectors.length} vectors -> ${target}`)
for (const v of vectors) console.log(`  ${v.name.padEnd(24)} ${v.digest}`)
