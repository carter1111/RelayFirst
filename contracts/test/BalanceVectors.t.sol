// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {MerkleVectors} from "./MerkleVectors.sol";

/// @title BalanceVectorsTest — cross-language agreement for the points claim leaf
///
/// @dev Invariant A4: the balance root's leaf (MVP.md §6.2b) must be computed
/// identically in Go and in this contract. The leaf is verified against the same
/// preimage RelayPoints._leaf uses, here recomputed from the Go-emitted agent hash,
/// total and epoch. If the two encodings ever drift, this fails — which is the only
/// way to catch a leaf that is wrong by a byte but still verifies against itself.
///
/// Written without forge-std, like the other tests here, so it runs offline.
contract BalanceVectorsTest {
    /// @dev The claim leaf, matching RelayPoints._leaf and internal/merkle.BalanceLeaf.
    function leaf(bytes32 agentHash, uint256 total, uint64 epoch) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(agentHash, total, epoch));
    }

    /// @notice Every Go-generated balance vector's leaf must match this contract's.
    function test_BalanceLeavesMatchGoGeneratedVectors() public pure {
        MerkleVectors.BalanceVector[] memory v = MerkleVectors.balanceVectors();
        require(v.length == MerkleVectors.BALANCE_COUNT, "vector count mismatch");

        for (uint256 i = 0; i < v.length; i++) {
            bytes32 got = leaf(v[i].agentHash, v[i].totalMicro, v[i].epoch);
            require(got == v[i].leaf, "balance leaf does not match the Go vectors");
        }
    }

    /// @notice The agent hash must be keccak256(chainId || address), 8 then 20 bytes.
    function test_AgentHashEncoding() public pure {
        // The first vector is agent:eip155:8453:0x7099...79c8. Recompute it here so
        // the agent-id derivation is gated too, not only the final leaf.
        bytes32 want = keccak256(abi.encodePacked(uint64(8453), address(0x70997970C51812dc3A010C7d01b50e0d17dc79C8)));

        MerkleVectors.BalanceVector[] memory v = MerkleVectors.balanceVectors();
        require(v[0].agentHash == want, "agent hash is not keccak256(chainId || address)");
    }
}
