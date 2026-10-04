// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title RelayBounty — anti-double-claim ledger for task bounties (S12-1, S12-2)
///
/// @notice Records which miner claimed which bounty, so the same work cannot be paid twice.
///
/// @dev # This contract holds NO assets, and that is the whole design
///
/// The obvious settlement contract escrows funds: the requester deposits, the contract pays out
/// on completion. MVP.md does not want that, and the reasons are explicit:
///
///   - The appendix says on-chain settlement was reduced from a full SettlementBatch to "one
///     mapping" (§1309).
///   - §12 says the USDC channel is a MINIMAL channel, not an x402 adapter.
///   - Most directly, §88 records what users actually fear: "funds held in escrow, not being able
///     to withdraw, KYC" — not insuﬀicient decentralization.
///
/// So this contract does the one thing that genuinely needs a shared, tamper-resistant record —
/// preventing a double claim — and does nothing else. The requester pays the miner directly. This
/// contract never holds, moves, or can confiscate a token.
///
/// @dev # Why a bitmap is the right primitive
///
/// The only question this contract must answer is "has this miner already claimed this bounty",
/// and the answer must be the same for everyone and must not be rewritable. A mapping from
/// (bountyId, minerId) to a boolean is exactly that, needs no state machine, and has no failure
/// mode in which funds can be stuck.
///
/// @dev # What it deliberately cannot do
///
///   - Cannot pay anyone. There is no transfer call in this file, so there is no path by which a
///     bug here could lose a user's money.
///   - Cannot un-claim. A claim is append-only, so a requester cannot revoke a miner's claim after
///     seeing it.
///   - Cannot decide WHETHER a claim is valid. Validity is the receipt's signature and the
///     verification outcome, checked off-chain. This contract records that a claim was made, not
///     that it was earned — the same split as the observation index (S10-1): index, do not judge.
contract RelayBounty {
    /// @notice Whether a miner has claimed a bounty.
    ///
    /// @dev Keyed by bounty id then miner id. The bounty id is chosen by the requester, so it is
    /// the requester's namespace; the miner id is a bytes32 agent id, matching how RelayPoints and
    /// the Go side derive it.
    mapping(bytes32 => mapping(bytes32 => bool)) public claimed;

    /// @notice How many miners have claimed each bounty.
    ///
    /// @dev Kept because "how many claimed" is what a requester reads to see whether a bounty is
    /// contested, and deriving it by scanning events is not possible on-chain.
    mapping(bytes32 => uint256) public claimCount;

    /// @notice Emitted once per (bounty, miner) pair, ever.
    event BountyClaimed(bytes32 indexed bountyId, bytes32 indexed minerId, bytes32 receiptId);

    error AlreadyClaimed(bytes32 bountyId, bytes32 minerId);

    /// @notice Records a claim, reverting if this miner already claimed this bounty.
    ///
    /// @dev The receipt id is recorded for traceability only: it lets an off-chain reader find the
    /// receipt a claim refers to without this contract having to understand receipts. It is not
    /// checked, because checking it would mean verifying a signature, which is the client's job
    /// (MVP.md §5.4) and cannot be done here.
    ///
    /// @param bountyId The requester's bounty identifier.
    /// @param minerId The claiming miner's agent id.
    /// @param receiptId The receipt the claim is based on, for off-chain traceability.
    function claim(bytes32 bountyId, bytes32 minerId, bytes32 receiptId) external {
        if (claimed[bountyId][minerId]) {
            revert AlreadyClaimed(bountyId, minerId);
        }
        claimed[bountyId][minerId] = true;
        claimCount[bountyId] += 1;
        emit BountyClaimed(bountyId, minerId, receiptId);
    }

    /// @notice Reports whether a miner may still claim a bounty.
    ///
    /// @dev Exposed so a caller does not have to interpret a revert to answer a yes/no question. A
    /// miner checking before doing work is the common case, and making them simulate a transaction
    /// for it would be needlessly expensive.
    function canClaim(bytes32 bountyId, bytes32 minerId) external view returns (bool) {
        return !claimed[bountyId][minerId];
    }
}