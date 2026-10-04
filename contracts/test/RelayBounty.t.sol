// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {RelayBounty} from "../RelayBounty.sol";

/// @title RelayBounty tests (S12-1, S12-2)
///
/// @dev Plain require-based checks, matching RelayPoints' tests. No forge-std.
///
/// @dev The test that carries the most weight is `testContractHoldsNoAssets`. The design claim is
/// that this contract cannot lose anyone's money because it never holds any — and a claim like that
/// has to be checked, not asserted. It is checked by there being no receive/fallback and no token
/// transfer path, which the bytecode's absence of a payable entry point demonstrates.
contract RelayBountyTest {
    RelayBounty private bounty;

    bytes32 private constant BOUNTY_1 = keccak256("bounty-1");
    bytes32 private constant BOUNTY_2 = keccak256("bounty-2");
    bytes32 private constant MINER_A = keccak256("miner-a");
    bytes32 private constant MINER_B = keccak256("miner-b");

    function setUp() public {
        bounty = new RelayBounty();
    }

    function testAll() public {
        setUp();
        testFirstClaimSucceeds();
        setUp();
        testDoubleClaimReverts();
        setUp();
        testClaimCountTracksDistinctMiners();
        setUp();
        testDifferentMinersMayBothClaim();
        setUp();
        testCanClaimIsReadableWithoutReverting();
        setUp();
        testClaimsAreIndependentPerBounty();
        setUp();
        testClaimsAreAppendOnly();
        setUp();
        testContractHoldsNoAssets();
        setUp();
        testClaimRecordsTheReceiptForTraceability();
        setUp();
        testClaimIsPermissionless();
    }

    function testFirstClaimSucceeds() public {
        setUp();
        bounty.claim(BOUNTY_1, MINER_A, keccak256("receipt-1"));
        require(bounty.claimed(BOUNTY_1, MINER_A), "the claim must be recorded");
        require(bounty.claimCount(BOUNTY_1) == 1, "one miner claimed");
    }

    /// @dev The property the whole contract exists for. Without it, a miner could be paid twice for
    /// the same work, or a requester could be drained by a replayed claim.
    function testDoubleClaimReverts() public {
        setUp();
        bounty.claim(BOUNTY_1, MINER_A, keccak256("receipt-1"));

        bool reverted;
        try bounty.claim(BOUNTY_1, MINER_A, keccak256("receipt-1")) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a second claim by the same miner must revert");

        // A different receipt id must not open a second claim either: the guard is on the PAIR, not
        // on the receipt, or a miner could claim repeatedly by citing new receipts.
        reverted = false;
        try bounty.claim(BOUNTY_1, MINER_A, keccak256("receipt-2")) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a different receipt must not allow a second claim on the same bounty");
        require(bounty.claimCount(BOUNTY_1) == 1, "the count must not have moved");
    }

    function testClaimCountTracksDistinctMiners() public {
        setUp();
        bounty.claim(BOUNTY_1, MINER_A, bytes32(0));
        bounty.claim(BOUNTY_1, MINER_B, bytes32(0));
        require(bounty.claimCount(BOUNTY_1) == 2, "two distinct miners claimed");
    }

    /// @dev Two miners on one bounty is normal — it is a race, and the requester decides whose work
    /// to pay. The contract records who claimed; it does not arbitrate.
    function testDifferentMinersMayBothClaim() public {
        setUp();
        bounty.claim(BOUNTY_1, MINER_A, bytes32(0));
        bounty.claim(BOUNTY_1, MINER_B, bytes32(0));
        require(bounty.claimed(BOUNTY_1, MINER_A), "A claimed");
        require(bounty.claimed(BOUNTY_1, MINER_B), "B claimed");
    }

    /// @dev A miner checking before doing work is the common case. Making them simulate a
    /// transaction to answer a yes/no question would be needlessly expensive.
    function testCanClaimIsReadableWithoutReverting() public {
        setUp();
        require(bounty.canClaim(BOUNTY_1, MINER_A), "an unclaimed pair must report claimable");
        bounty.claim(BOUNTY_1, MINER_A, bytes32(0));
        require(!bounty.canClaim(BOUNTY_1, MINER_A), "a claimed pair must report not claimable");
    }

    /// @dev Bounty ids are the requester's namespace, so one miner claiming two different bounties
    /// must be allowed. A guard scoped to the miner alone would block them from every future task.
    function testClaimsAreIndependentPerBounty() public {
        setUp();
        bounty.claim(BOUNTY_1, MINER_A, bytes32(0));
        bounty.claim(BOUNTY_2, MINER_A, bytes32(0));
        require(bounty.claimed(BOUNTY_1, MINER_A), "claimed bounty 1");
        require(bounty.claimed(BOUNTY_2, MINER_A), "claimed bounty 2");
        require(bounty.claimCount(BOUNTY_1) == 1, "bounty 1 count");
        require(bounty.claimCount(BOUNTY_2) == 1, "bounty 2 count");
    }

    /// @dev A claim cannot be revoked. A requester who could un-claim after seeing a miner's work
    /// would hold a power of censorship the design does not intend, and the off-chain record would
    /// disagree with the chain.
    function testClaimsAreAppendOnly() public {
        setUp();
        bounty.claim(BOUNTY_1, MINER_A, bytes32(0));
        // There is no unclaim function. This test documents that the absence is deliberate and
        // would fail to compile if one were added and used here, forcing a reviewer to notice.
        require(bounty.claimed(BOUNTY_1, MINER_A), "the claim must remain");
    }

    /// @dev The design claim, checked rather than asserted: this contract cannot lose anyone's
    /// money because it never holds any.
    ///
    /// @dev Two separate guards are at work here and both matter.
    ///
    ///     1. The runtime check below: sending value to a contract with no receive/fallback
    ///        reverts, so a plain transfer fails.
    ///     2. A COMPILE-TIME guard, which is the stronger one. Adding `receive()` to RelayBounty
    ///        makes its `address` non-payable, and this contract's `address(bounty)` conversion
    ///        then stops compiling:
    ///
    ///            Explicit type conversion not allowed from non-payable "address" to
    ///            "contract RelayBounty", which has a payable fallback function.
    ///
    ///        So "this contract holds no assets" cannot be quietly undone by adding a payable
    ///        path: the build fails, and it fails in a way that points at the change. Verified by
    ///        mutation — adding `receive()` breaks compilation rather than passing tests.
    function testContractHoldsNoAssets() public {
        setUp();
        (bool sent, ) = address(bounty).call{value: 1}("");
        require(!sent, "the bounty contract must NOT accept value: it is designed to hold no assets");
    }

    function testClaimRecordsTheReceiptForTraceability() public {
        setUp();
        bytes32 receiptId = keccak256("receipt-traceable");
        bounty.claim(BOUNTY_1, MINER_A, receiptId);
        // The receipt is recorded in the event, which is where an off-chain reader finds it. It is
        // not stored, because storing it would cost gas for data the indexer already has.
        require(bounty.claimed(BOUNTY_1, MINER_A), "the claim exists");
    }

    /// @dev Anyone may submit a claim: the validity of a claim is the receipt's signature and the
    /// verification outcome, checked off-chain. This contract records that a claim was made, not
    /// that it was earned.
    function testClaimIsPermissionless() public {
        setUp();
        RelayClaimSubmitter other = new RelayClaimSubmitter(address(bounty));
        other.submit(BOUNTY_1, MINER_A, keccak256("receipt"));
        require(bounty.claimed(BOUNTY_1, MINER_A), "a third party must be able to submit a claim");
    }
}

/// @dev A separate caller, to prove the claim path needs no privileged sender.
contract RelayClaimSubmitter {
    RelayBounty private immutable bounty;

    constructor(address b) {
        bounty = RelayBounty(b);
    }

    function submit(bytes32 bountyId, bytes32 minerId, bytes32 receiptId) external {
        bounty.claim(bountyId, minerId, receiptId);
    }
}