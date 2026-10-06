// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {RelayPoints} from "../RelayPoints.sol";
import {RelayAnchor} from "../RelayAnchor.sol";
import {IERC721Receiver} from "openzeppelin-contracts/contracts/token/ERC721/IERC721Receiver.sol";

/// @title RelayPoints × RelayAnchor — the claim against a REAL anchor (S11-8, criterion ⑨)
///
/// @dev Why this file exists, stated plainly: the other RelayPoints tests use a stub root
/// source that answers "yes" to every proof. That is enough to test the claim's accounting,
/// but it cannot catch the failure that matters most for a claim — that the LEAF the badge
/// computes and the leaf the anchor hashes are not the same shape, or that the proof layout
/// the two sides expect differs. A stub agrees with whatever you give it; a real anchor does
/// not. So this wires the two real contracts together and proves a claim end to end.
///
/// @dev Plain require-based checks, matching the sibling tests. No forge-std, so no
/// cheatcodes: a small local Merkle implementation stands in for the off-chain prover, and its
/// leaves are computed with the SAME formula RelayPoints uses (`keccak256(abi.encodePacked(
/// agentId, total, epoch))`), which is the point — if the two ever disagree, this test fails.
contract RelayPointsAnchorTest is IERC721Receiver {
    RelayAnchor private anchor;
    RelayPoints private points;

    function onERC721Received(address, address, uint256, bytes calldata) external pure returns (bytes4) {
        return IERC721Receiver.onERC721Received.selector;
    }

    function setUp() public {
        anchor = new RelayAnchor();
        // RelayPoints proves against the operator's roots in the real anchor.
        points = new RelayPoints(address(anchor), address(this), "RelayFirst Points", "RFP");
    }

    function testAll() public {
        setUp();
        testClaimAgainstRealAnchorRoot();
        setUp();
        testClaimRejectsAProofForAnotherEpochsRoot();
        setUp();
        testClaimRejectsWhenNoRootWasSubmitted();
    }

    // ---------------------------------------------------------------- end to end

    /// @dev The acceptance property: a root is submitted to a real RelayAnchor, and a claim
    /// against it succeeds. Before this, "the claim works" was only shown against a stub.
    function testClaimAgainstRealAnchorRoot() public {
        bytes32 agentId = keccak256("agent:alice");
        uint256 total = 150;
        uint64 epoch = 2;

        // Two leaves, so the proof is a real fold rather than a single leaf. The tree uses
        // RelayAnchor's own hashing: leafHash(leaf) = keccak256(0x00 || leaf) and
        // nodeHash(l, r) = keccak256(0x01 || l || r), with the index deciding which side a
        // node goes on. Reproducing it here is deliberate -- if this ever stops matching the
        // contract, the end-to-end claim fails, which is the alarm we want.
        bytes32 leafA = _leaf(keccak256("agent:bob"), 40, epoch); // index 0
        bytes32 leafB = _leaf(agentId, total, epoch); // index 1
        bytes32 hA = _leafHash(leafA);
        bytes32 hB = _leafHash(leafB);
        bytes32 root = _nodeHash(hA, hB);
        uint256 width = 2;

        // The operator publishes the root on chain through the real contract.
        anchor.submitRoot(epoch, root, width);

        // Alice's proof: her leaf is at index 1, so its sibling is leaf A's hash.
        bytes32[] memory proof = new bytes32[](1);
        proof[0] = hA;

        // And the badge mints. If RelayPoints hashed its leaf differently from the prover
        // above, this would revert with NotClaimable.
        uint256 tokenId = points.claimBadge(8453);
        points.claimPoints(agentId, total, epoch, 1, proof);

        require(points.pointsOf(agentId) == total, "the claimed total must be recorded");
        require(points.ownerOf(tokenId) == address(this), "the badge went to the claimer");
    }

    /// @dev A proof valid for one epoch must not claim against another enumerator's root. Two
    /// roots are submitted; a proof that folds to the epoch-2 root is offered at epoch 3.
    function testClaimRejectsAProofForAnotherEpochsRoot() public {
        bytes32 agentId = keccak256("agent:carol");
        uint256 total = 20;

        bytes32 leafE2 = _leaf(agentId, total, 2);
        // A single-leaf tree: width 1, an empty proof, and the root is the LEAF HASH (the
        // contract folds a bare leaf by hashing it once with domain separation).
        anchor.submitRoot(2, _leafHash(leafE2), 1);
        // A different root for epoch 3.
        anchor.submitRoot(3, keccak256("other-root"), 1);

        points.claimBadge(8453);
        bytes32[] memory empty = new bytes32[](0);

        // Claiming epoch 3 with epoch 2's leaf must fail: a wrong epoch is exactly the case
        // the message names, and a stub could not distinguish it.
        bool reverted;
        try points.claimPoints(agentId, total, 3, 0, empty) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a proof for another epoch's root must be refused");

        // And the same leaf at its own epoch succeeds, so the refusal above is about the
        // epoch and not about the leaf being wrong in general.
        points.claimPoints(agentId, total, 2, 0, empty);
        require(points.pointsOf(agentId) == total, "the correct-epoch claim must set the total");
    }

    /// @dev Before any root is published, the anchor returns false, and a claim must fail
    /// rather than succeed against a zero root.
    function testClaimRejectsWhenNoRootWasSubmitted() public {
        points.claimBadge(8453);
        bytes32[] memory empty = new bytes32[](0);
        bool reverted;
        try points.claimPoints(keccak256("agent:dave"), 10, 9, 0, empty) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a claim with no published root must be refused");
    }

    // ---------------------------------------------------------------- local prover

    /// @dev The leaf formula, copied from RelayPoints._leaf ON PURPOSE. If the contract changes
    /// its encoding, this stops matching and the end-to-end test fails — which is the alarm we
    /// want, because a leaf-encoding change silently invalidates every published proof.
    function _leaf(bytes32 agentId, uint256 total, uint64 epoch) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(agentId, total, epoch));
    }

    /// @dev RelayAnchor's leaf/node hashing, copied so the prover matches the verifier.
    function _leafHash(bytes32 id) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(bytes1(0x00), id));
    }

    function _nodeHash(bytes32 left, bytes32 right) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(bytes1(0x01), left, right));
    }
}
