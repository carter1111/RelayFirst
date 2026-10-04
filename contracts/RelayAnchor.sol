// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title RelayAnchor — epoch root anchoring for RelayFirst (MVP.md §4.3)
///
/// @notice Records one Merkle root per epoch so anyone can later prove a specific
/// receipt was included in that epoch. This is the whole on-chain surface of the
/// MVP, and it is deliberately tiny.
///
/// @dev What this contract deliberately does NOT do, and why it matters:
///
///   - No settlement, escrow, claim, withdraw or dispute logic. Points are
///     non-transferable, unpriced and carry no promised return (invariant A5, MVP.md
///     §6.1). The moment a contract can move value on the basis of a receipt, the
///     "we never issue a token" escape hatch closes. Those features belong to
///     ARCHITECTURE.md as roadmap, not here.
///   - No receipt verification. A root is a commitment, not a proof of validity:
///     the contract cannot know whether the receipts behind a root represent real
///     work. Verification is the client's job (MVP.md §5.4).
///   - No reward of any kind for submitting a root. Submitting is a public good
///     performed with the submitter's own gas.
///
/// @dev The Merkle construction mirrors internal/merkle in Go, and the two are
/// checked against shared vectors in CI (invariant A4). A verifier that agrees only
/// with itself proves nothing, so neither side is authoritative alone.
///
///       leaf(id)         = keccak256(0x00 ‖ id)
///       node(left,right) = keccak256(0x01 ‖ left ‖ right)
///
///     The single prefix byte is load-bearing: without it a leaf and an internal
///     node are both "some 32-byte hash", so an attacker could present an internal
///     node as a leaf and obtain a valid proof for a receipt that was never
///     included.
contract RelayAnchor {
    /// @notice The Merkle root recorded for each epoch.
    ///
    /// @dev Keyed by epoch AND guarded by msg.sender below. A single shared mapping
    /// would let anyone overwrite any other operator's root, which would make the
    /// anchor worthless as evidence. Namespacing per submitter is the minimal fix
    /// and keeps the contract free of any owner, admin or registry — there is no
    /// privileged party here, by design.
    mapping(address operator => mapping(uint256 epoch => bytes32 receiptsRoot)) public roots;

    /// @notice When each root was recorded, for auditing.
    mapping(address operator => mapping(uint256 epoch => uint64 submittedAt)) public submittedAt;

    /// @notice The padded leaf count used to build the epoch's tree.
    ///
    /// @dev Recorded because the tree shape must be reproducible off-chain: the Go
    /// side pads to a power of two, and a verifier that guessed a different width
    /// would compute a different root. Storing it makes the shape explicit rather
    /// than a convention two implementations have to remember separately.
    mapping(address operator => mapping(uint256 epoch => uint256 width)) public widthOf;

    /// @notice Emitted when a root is recorded.
    event RootSubmitted(
        address indexed operator,
        uint256 indexed epoch,
        bytes32 receiptsRoot,
        uint256 width,
        uint64 submittedAt
    );

    /// @notice Thrown when an epoch already has a root for this operator.
    error EpochAlreadyAnchored(address operator, uint256 epoch, bytes32 existing);

    /// @notice Thrown when a zero root is submitted.
    error EmptyRootNotAllowed();

    /// @notice Thrown when a proof is structurally inconsistent.
    error MalformedProof();

    /// @notice Thrown when a proof does not reproduce the recorded root.
    error ProofDoesNotMatch(bytes32 computed, bytes32 recorded);

    /// @notice Record the Merkle root for one epoch.
    ///
    /// @dev One root per (operator, epoch) and no updates. An epoch is a finished
    /// window of work; allowing a rewrite would mean the anchor could be changed
    /// after the fact, which is precisely what anchoring exists to prevent. If a
    /// superseding anchor is ever needed it should be a new epoch, not an edit.
    ///
    /// @param epoch  The epoch index, as computed by the Go side from the shared
    ///               genesis (internal/epoch).
    /// @param root   The Merkle root over that epoch's receipt ids.
    /// @param width  The padded leaf count, a power of two.
    function submitRoot(uint256 epoch, bytes32 root, uint256 width) external {
        if (root == bytes32(0)) revert EmptyRootNotAllowed();
        if (width == 0 || (width & (width - 1)) != 0) revert MalformedProof();

        bytes32 existing = roots[msg.sender][epoch];
        if (existing != bytes32(0)) revert EpochAlreadyAnchored(msg.sender, epoch, existing);

        roots[msg.sender][epoch] = root;
        widthOf[msg.sender][epoch] = width;
        submittedAt[msg.sender][epoch] = uint64(block.timestamp);

        emit RootSubmitted(msg.sender, epoch, root, width, uint64(block.timestamp));
    }

    /// @notice Verify that a receipt id is included in an operator's epoch root.
    ///
    /// @dev Mirrors the Go verifier exactly. `siblings.length` must be log2(width);
    /// a shorter or longer list describes no real tree, so it is rejected up front
    /// rather than folded into a hash that might coincidentally match.
    ///
    /// @param operator The operator whose root is being checked.
    /// @param epoch    The epoch to check.
    /// @param leaf     The 32-byte receipt id.
    /// @param index    The leaf's position in the epoch's ordering.
    /// @param siblings The sibling hashes from the leaf level up to the root.
    /// @return ok True when the proof reproduces the recorded root.
    function verifyProof(
        address operator,
        uint256 epoch,
        bytes32 leaf,
        uint256 index,
        bytes32[] calldata siblings
    ) public view returns (bool ok) {
        bytes32 root = roots[operator][epoch];
        if (root == bytes32(0)) return false;

        uint256 width = widthOf[operator][epoch];
        if (!proofShapeOk(width, index, siblings.length)) return false;

        return foldProof(leaf, index, siblings) == root;
    }

    /// @notice Same check as verifyProof, but reverts with a reason instead of
    ///         returning false, so a caller can tell "no root" from "bad proof".
    function requireProof(
        address operator,
        uint256 epoch,
        bytes32 leaf,
        uint256 index,
        bytes32[] calldata siblings
    ) external view {
        bytes32 root = roots[operator][epoch];
        if (root == bytes32(0)) revert MalformedProof();

        uint256 width = widthOf[operator][epoch];
        if (!proofShapeOk(width, index, siblings.length)) revert MalformedProof();

        bytes32 computed = foldProof(leaf, index, siblings);
        if (computed != root) revert ProofDoesNotMatch(computed, root);
    }

    /// @notice Recomputes an epoch's root from a leaf and its proof path.
    ///
    /// @dev Exposed for testing against the Go implementation. It is `pure`, so it
    /// costs nothing to compare the two sides.
    function foldProof(
        bytes32 leaf,
        uint256 index,
        bytes32[] calldata siblings
    ) public pure returns (bytes32) {
        bytes32 h = leafHash(leaf);
        uint256 pos = index;

        for (uint256 i = 0; i < siblings.length; i++) {
            bytes32 sibling = siblings[i];
            // Even positions are a left child, so the running hash goes on the left.
            // Getting this backwards produces a tree that verifies nothing, but only
            // for odd indices, which is the kind of bug that survives casual testing.
            h = (pos & 1 == 0) ? nodeHash(h, sibling) : nodeHash(sibling, h);
            pos >>= 1;
        }
        return h;
    }

    /// @notice Hash a receipt id into a leaf, with domain separation.
    function leafHash(bytes32 id) public pure returns (bytes32) {
        return keccak256(abi.encodePacked(bytes1(0x00), id));
    }

    /// @notice Hash two child nodes, with domain separation.
    function nodeHash(bytes32 left, bytes32 right) public pure returns (bytes32) {
        return keccak256(abi.encodePacked(bytes1(0x01), left, right));
    }

    /// @dev Shape validation, shared by both verification entry points.
    function proofShapeOk(uint256 width, uint256 index, uint256 siblingCount) internal pure returns (bool) {
        if (width == 0) return false;
        // Width must be a power of two, the same invariant the Go side enforces.
        if ((width & (width - 1)) != 0) return false;
        if (index >= width) return false;
        return siblingCount == log2(width);
    }

    /// @dev log2 of a power of two.
    function log2(uint256 n) internal pure returns (uint256 k) {
        while (n > 1) {
            n >>= 1;
            k++;
        }
    }
}
