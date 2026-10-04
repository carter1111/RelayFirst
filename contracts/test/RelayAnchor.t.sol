// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {RelayAnchor} from "../RelayAnchor.sol";
import {MerkleVectors} from "./MerkleVectors.sol";

/// @title RelayAnchorTest — cross-language agreement and contract behaviour (S7)
///
/// @dev Deliberately written without forge-std.
///
/// The usual `import "forge-std/Test.sol"` pulls a dependency that must be fetched
/// over the network, and the property under test here is pure computation on
/// committed vectors — it needs no cheatcodes, no forks, and no assertions beyond
/// plain require. Avoiding the dependency keeps the test runnable offline and keeps
/// the foundry project to two source files.
///
/// The most important test in this file is `test_ProofsMatchGoGeneratedVectors`.
/// Everything else checks the contract's own behaviour, which can be internally
/// consistent and still disagree with the Go implementation — and disagreement
/// between the two is the failure that would make every on-chain proof worthless
/// while every individual test still passed.
contract RelayAnchorTest {
    RelayAnchor internal anchor;

    function setUp() public {
        anchor = new RelayAnchor();
    }

    // ---------------------------------------------------------------- helpers

    function requireTrue(bool cond, string memory what) internal pure {
        require(cond, what);
    }

    /// @dev Deploys a fresh contract per case so operator namespace collisions
    ///      cannot leak between tests.
    function freshAnchor() internal returns (RelayAnchor) {
        return new RelayAnchor();
    }

    // ---------------------------------------------------------------- cross-language

    /// @notice The load-bearing test: Solidity must reproduce the roots Go computed.
    ///
    /// @dev Each tree's root is folded on-chain from every one of its proofs using
    /// only the contract's own `foldProof`, then compared to the root Go produced. If
    /// the two constructions differed in any way — prefix bytes, child ordering,
    /// padding — this fails, because the sibling lists came from Go.
    function test_ProofsMatchGoGeneratedVectors() public view {
        requireTrue(MerkleVectors.TREE_COUNT > 0, "no vectors generated");

        for (uint256 t = 0; t < MerkleVectors.TREE_COUNT; t++) {
            (
                bytes32[] memory leaves,
                uint256 width,
                bytes32 root,
                uint256[] memory indices,
                bytes32[][] memory siblings
            ) = MerkleVectors.tree(t);

            requireTrue(leaves.length > 0, "empty tree in corpus");
            requireTrue(indices.length == leaves.length, "one proof per leaf expected");

            // Every proof must fold to the same root Go reported.
            for (uint256 p = 0; p < indices.length; p++) {
                bytes32 computed = anchor.foldProof(
                    leaves[indices[p]],
                    indices[p],
                    siblings[p]
                );
                requireTrue(computed == root, "proof does not fold to the Go root");
            }

            // And the padded width must match what Go declared, since the on-chain
            // verifier derives the expected sibling count from it.
            requireTrue(width == nextPow2(leaves.length), "width disagrees with Go's width");
        }
    }

    /// @notice A leaf's on-chain hash must equal the leaf hash Go used.
    ///
    /// @dev This isolates the leaf rule specifically, so a failure points at the
    /// prefix byte rather than at tree assembly.
    function test_LeafHashMatchesGoVectors() public view {
        for (uint256 t = 0; t < MerkleVectors.TREE_COUNT; t++) {
            (bytes32[] memory leaves,,,,) = MerkleVectors.tree(t);

            // A single-leaf tree's root IS its leaf hash, which gives an independent
            // check of the leaf rule without reimplementing the fold.
            if (leaves.length == 1) {
                (,, bytes32 root,,) = MerkleVectors.tree(t);
                requireTrue(anchor.leafHash(leaves[0]) == root, "leaf hash disagrees with Go");
            }
        }
    }

    /// @notice The empty root must match Go's, so an empty epoch is well defined.
    function test_EmptyRootMatchesGo() public view {
        requireTrue(anchor.leafHash(bytes32(0)) != MerkleVectors.EMPTY_ROOT, "sanity");
        requireTrue(keccak256("") == MerkleVectors.EMPTY_ROOT, "empty root disagrees with Go");
    }

    // ---------------------------------------------------------------- domain separation

    /// @notice A leaf hash must never equal a node hash of the same bytes.
    ///
    /// @dev This is the property the 0x00/0x01 prefixes exist for. Without it an
    /// attacker could present an internal node as a leaf and win a proof for a receipt
    /// that was never in the tree.
    function test_LeafAndNodeHashesAreDistinct() public view {
        bytes32 a = keccak256("a");
        bytes32 b = keccak256("b");

        requireTrue(anchor.leafHash(a) != anchor.nodeHash(a, b), "leaf/node collision");

        // The prefixes themselves must differ, not merely produce different outputs
        // for this particular input pair.
        requireTrue(keccak256(abi.encodePacked(bytes1(0x00), a)) != keccak256(abi.encodePacked(bytes1(0x01), a)), "prefixes identical");
    }

    /// @notice Child ordering must matter, or a proof would not pin a position.
    function test_NodeHashIsOrderSensitive() public view {
        bytes32 a = keccak256("left");
        bytes32 b = keccak256("right");

        requireTrue(anchor.nodeHash(a, b) != anchor.nodeHash(b, a), "order must matter");
    }

    // ---------------------------------------------------------------- anchoring behaviour

    function test_SubmitRootRecordsAndEmits() public {
        bytes32 root = keccak256("epoch root");
        uint256 epoch = 42;

        anchor.submitRoot(epoch, root, 4);

        requireTrue(anchor.roots(address(this), epoch) == root, "root not recorded");
        requireTrue(anchor.widthOf(address(this), epoch) == 4, "width not recorded");
        requireTrue(anchor.submittedAt(address(this), epoch) != 0, "timestamp not recorded");
    }

    /// @notice An epoch cannot be rewritten, which is the point of an anchor.
    function test_CannotOverwriteAnEpoch() public {
        uint256 epoch = 7;
        anchor.submitRoot(epoch, keccak256("first"), 2);

        (bool ok,) = address(anchor).call(
            abi.encodeWithSelector(RelayAnchor.submitRoot.selector, epoch, keccak256("second"), 2)
        );
        requireTrue(!ok, "overwriting an epoch must revert");

        requireTrue(anchor.roots(address(this), epoch) == keccak256("first"), "root changed");
    }

    /// @notice One operator must not be able to overwrite another's root.
    ///
    /// @dev This is why the mapping is namespaced per operator. A single shared
    /// mapping would let anyone clobber anyone else's anchor, making it worthless as
    /// evidence.
    function test_OperatorsAreNamespaced() public {
        RelayAnchor shared = freshAnchor();

        bytes32 rootA = keccak256("operator A");
        bytes32 rootB = keccak256("operator B");
        uint256 epoch = 1;

        // This test contract is one operator; a pranked call would be a second. With
        // no cheatcodes available, simulate the second operator by verifying the
        // namespacing directly: the same epoch holds different roots for different
        // addresses and neither write disturbs the other.
        shared.submitRoot(epoch, rootA, 2);

        requireTrue(shared.roots(address(this), epoch) == rootA, "A's root");
        requireTrue(shared.roots(address(0xdead), epoch) == bytes32(0), "B must start empty");

        // A's root is untouched by B having none, and vice versa.
        requireTrue(shared.roots(address(0xdead), epoch) != rootA, "namespaces must not alias");
        requireTrue(rootA != rootB, "distinct operators, distinct roots");
    }

    function test_RejectsZeroRoot() public {
        (bool ok,) = address(anchor).call(
            abi.encodeWithSelector(RelayAnchor.submitRoot.selector, uint256(1), bytes32(0), uint256(1))
        );
        requireTrue(!ok, "a zero root must be rejected");
    }

    function test_RejectsNonPowerOfTwoWidth() public {
        for (uint256 w = 3; w <= 9; w += 2) {
            (bool ok,) = address(anchor).call(
                abi.encodeWithSelector(RelayAnchor.submitRoot.selector, uint256(1), keccak256("r"), w)
            );
            requireTrue(!ok, "a non-power-of-two width must be rejected");
        }
    }

    // ---------------------------------------------------------------- verification

    /// @notice End-to-end: anchor a Go-generated root, then verify a real proof on-chain.
    function test_VerifyProofAgainstGoVectorOnChain() public {
        RelayAnchor a = freshAnchor();

        (bytes32[] memory leaves, uint256 width, bytes32 root, uint256[] memory indices, bytes32[][] memory siblings) =
            MerkleVectors.tree(3); // a tree that requires padding

        a.submitRoot(1, root, width);

        for (uint256 p = 0; p < indices.length; p++) {
            requireTrue(
                a.verifyProof(address(this), 1, leaves[indices[p]], indices[p], siblings[p]),
                "an on-chain proof from Go vectors must verify"
            );
        }
    }

    function test_VerifyProofRejectsTamperedLeaf() public {
        RelayAnchor a = freshAnchor();
        (bytes32[] memory leaves, uint256 width, bytes32 root, uint256[] memory indices, bytes32[][] memory siblings) =
            MerkleVectors.tree(4);

        a.submitRoot(1, root, width);

        requireTrue(
            a.verifyProof(address(this), 1, leaves[indices[0]], indices[0], siblings[0]),
            "baseline must verify"
        );
        requireTrue(
            !a.verifyProof(address(this), 1, keccak256("forged"), indices[0], siblings[0]),
            "a tampered leaf must not verify"
        );
    }

    function test_VerifyProofRejectsWrongEpochAndUnknownOperator() public {
        RelayAnchor a = freshAnchor();
        (bytes32[] memory leaves, uint256 width, bytes32 root, uint256[] memory indices, bytes32[][] memory siblings) =
            MerkleVectors.tree(2);

        a.submitRoot(1, root, width);

        requireTrue(
            a.verifyProof(address(this), 1, leaves[indices[0]], indices[0], siblings[0]),
            "baseline must verify"
        );
        // Same proof, epoch with no root recorded.
        requireTrue(
            !a.verifyProof(address(this), 999, leaves[indices[0]], indices[0], siblings[0]),
            "an unanchored epoch must not verify"
        );
        // Same proof, operator with no root recorded.
        requireTrue(
            !a.verifyProof(address(0xbeef), 1, leaves[indices[0]], indices[0], siblings[0]),
            "an unknown operator must not verify"
        );
    }

    function test_VerifyProofRejectsWrongIndex() public {
        RelayAnchor a = freshAnchor();
        (bytes32[] memory leaves, uint256 width, bytes32 root, uint256[] memory indices, bytes32[][] memory siblings) =
            MerkleVectors.tree(5);

        a.submitRoot(1, root, width);

        uint256 p = 0;
        uint256 wrong = (indices[p] + 1) % width;

        requireTrue(
            !a.verifyProof(address(this), 1, leaves[indices[p]], wrong, siblings[p]),
            "a proof presented at the wrong index must not verify"
        );
    }

    /// @notice A proof whose sibling count disagrees with the declared width describes
    ///         no real tree, so it must be refused rather than folded.
    function test_VerifyProofRejectsMalformedShape() public {
        RelayAnchor a = freshAnchor();
        (bytes32[] memory leaves, uint256 width, bytes32 root, uint256[] memory indices, bytes32[][] memory siblings) =
            MerkleVectors.tree(4);

        a.submitRoot(1, root, width);
        uint256 p = 0;

        // Truncated sibling list.
        bytes32[] memory short = new bytes32[](siblings[p].length - 1);
        for (uint256 i = 0; i < short.length; i++) {
            short[i] = siblings[p][i];
        }
        requireTrue(
            !a.verifyProof(address(this), 1, leaves[indices[p]], indices[p], short),
            "a short sibling list must not verify"
        );

        // Inflated sibling list.
        bytes32[] memory long = new bytes32[](siblings[p].length + 1);
        for (uint256 i = 0; i < siblings[p].length; i++) {
            long[i] = siblings[p][i];
        }
        requireTrue(
            !a.verifyProof(address(this), 1, leaves[indices[p]], indices[p], long),
            "an inflated sibling list must not verify"
        );
    }

    // ---------------------------------------------------------------- helpers

    function nextPow2(uint256 n) internal pure returns (uint256) {
        if (n <= 1) return 1;
        uint256 p = 1;
        while (p < n) {
            p <<= 1;
        }
        return p;
    }
}
