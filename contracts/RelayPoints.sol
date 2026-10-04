// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {ERC721} from "openzeppelin-contracts/contracts/token/ERC721/ERC721.sol";
import {IERC5192} from "./interfaces/IERC5192.sol";

/// @title RelayPoints — non-transferable points badge (MVP.md §6.1, invariant A5)
///
/// @notice One badge per agent, carrying a cumulative points total that is updated by Merkle
/// claim. The badge is the agent's identity; points are a number on it, not a second token.
///
/// @dev What this contract deliberately does NOT do, and why each omission is load-bearing:
///
///   - No price, no sale, no royalty, no market function. Points are unpriced and carry no promised return,
///     so nothing here can be bought or sold. The moment this contract can move value on the basis of a
///     points balance, the "we never issue a token" escape hatch closes (invariant A5).
///   - No transfer, and not merely a `locked()` declaration. ERC-5192's `locked()` is a
///     DECLARATION: it reports that a token should not move, and enforces nothing. A
///     marketplace that ignores the flag can still transfer the token. The enforcement has to
///     be in `_update`, which is why that override exists below and why a test asserts a real
///     transfer reverts rather than reading the flag.
///   - No per-claim minting. Minting a badge per claim would make each award a separate NFT,
///     turning a cumulative score into a collection to trade around. The total lives in a
///     mapping and never moves.
///   - No `renounceRole`-style escape either: there is no privileged role at all, so there is
///     nothing to renounce and no key that could be used to mint points arbitrarily. Roots are
///     published by the existing RelayAnchor contract, and this one reads claims against them.
///
/// @dev Why cumulative rather than incremental. If a claim credited the delta since the last
/// claim, a user who missed one would lose those points permanently — the total would never
/// include them. So a claim sets the total to the claimed value, and a missed claim costs only
/// the delay, because the next claim includes everything.
contract RelayPoints is ERC721, IERC5192 {
    /// @notice The anchor contract that holds the epoch roots claims are proven against.
    RelayRootSource public immutable roots;

    /// @notice The operator whose published roots this collection accepts claims against.
    ///
    /// @dev It is fixed at deployment and not settable. A mutable operator would let the
    /// deployer redirect claims to roots they chose after the fact, which is the authority this
    /// design deliberately does not have. Rotating operators means deploying a new collection,
    /// which is visible and cannot rewrite anyone's existing total.
    address public immutable operator;

    /// @notice Cumulative points per agent.
    ///
    /// @dev The value is the total ever earned, never a delta. See the contract comment: an
    /// incremental design loses points when a claim is missed, which is a design defect rather
    /// than a user error.
    mapping(bytes32 => uint256) private _points;

    /// @notice The token held by an agent, once minted.
    ///
    /// @dev The agent id is the key rather than the token id, because an agent must be able to
    /// look itself up: the claim flow starts from "who am I", not from a token number.
    mapping(bytes32 => uint256) private _tokenOf;

    /// @notice How many badges exist.
    uint256 public totalBadges;

    /// @notice Whether a badge has ever been minted for an agent.
    event BadgeMinted(bytes32 indexed agentId, uint256 indexed tokenId);
    /// @notice Emitted when an agent's cumulative total is updated by a claim.
    event PointsClaimed(bytes32 indexed agentId, uint256 previousTotal, uint256 newTotal);

    error AlreadyMinted(bytes32 agentId);
    error NotClaimable(string reason);
    error SoulboundNonTransferable();

    /// @param rootSource The RelayAnchor deployment holding epoch roots.
    /// @param name ERC-721 collection name.
    /// @param symbol ERC-721 collection symbol.
    constructor(address rootSource, address rootOperator, string memory name, string memory symbol)
        ERC721(name, symbol)
    {
        roots = RelayRootSource(rootSource);
        operator = rootOperator;
    }

    /// @notice Mints the calling agent's badge.
    ///
    /// @dev Identity is NOT taken from `msg.sender` alone. A badge is claimed by an agent for
    /// an EVM address, and binding them together is what makes the badge an identity rather
    /// than a collectible someone else can mint on your behalf. The agent id is derived from
    /// the claimer's address, so a caller cannot mint a badge for an address it does not
    /// control.
    ///
    /// @param chainId The EVM chain the caller's identity is anchored to. It is a parameter
    /// rather than a constant because the same address is a different agent on a different
    /// chain, and hardcoding one would silently merge identities across chains.
    /// @return tokenId The badge's token id.
    function claimBadge(uint64 chainId) external returns (uint256 tokenId) {
        bytes32 agentId = _agentId(chainId, msg.sender);
        if (_tokenOf[agentId] != 0) {
            revert AlreadyMinted(agentId);
        }
        totalBadges += 1;
        tokenId = totalBadges;
        _tokenOf[agentId] = tokenId;
        _safeMint(msg.sender, tokenId);
        emit BadgeMinted(agentId, tokenId);
        // ERC-5192 requires the lock event on mint, since a soulbound token is locked from the
        // moment it exists rather than locked later.
        emit Locked(tokenId);
    }

    /// @notice Returns an agent's cumulative points.
    function pointsOf(bytes32 agentId) external view returns (uint256) {
        return _points[agentId];
    }

    /// @notice Returns the badge token id for an agent, or 0 when none was minted.
    function badgeOf(bytes32 agentId) external view returns (uint256) {
        return _tokenOf[agentId];
    }

    /// @notice Returns the agent id for an EVM address on a chain.
    ///
    /// @dev Exposed so a client does not have to reimplement the derivation and risk
    /// disagreeing with the contract about what an agent is.
    function agentIdFor(uint64 chainId, address owner) external pure returns (bytes32) {
        return _agentId(chainId, owner);
    }

    /// @notice Sets an agent's total to a value proven against a published epoch root.
    ///
    /// @dev Why a CLAIM sets the total instead of adding to it: the proof asserts "this agent
    /// earned N by this epoch", and N is already cumulative. Adding would double-count every
    /// earlier claim, and crediting a delta would lose points when a claim is skipped. Setting
    /// the total is the only one of the three that is correct when claims are missed, which
    /// they will be.
    ///
    /// @dev Why a claim can never LOWER the total: a later root is a superset of earlier ones
    /// in what it accounts for, so a lower value would mean a client proved the wrong epoch or
    /// the off-chain ledger was rebuilt inconsistently. Reverting is the honest response,
    /// because silently accepting it would erase points that were already earned.
    ///
    /// @param agentId The agent the claim is about.
    /// @param total The cumulative total being claimed.
    /// @param epoch The epoch the proof is for.
    /// @param index The leaf's position in the epoch ordering.
    /// @param proof Merkle proof against the epoch root.
    function claimPoints(
        bytes32 agentId,
        uint256 total,
        uint64 epoch,
        uint256 index,
        bytes32[] calldata proof
    ) external {
        bytes32 leaf = _leaf(agentId, total, epoch);
        // verifyProof returns false rather than reverting, so the reason is attached here
        // instead: a bare false would leave a claimant unable to tell a wrong epoch from a
        // wrong proof from a total that was never earned.
        if (!roots.verifyProof(operator, epoch, leaf, index, proof)) {
            revert NotClaimable("proof does not match the recorded epoch root");
        }
        uint256 previous = _points[agentId];
        if (total < previous) {
            revert NotClaimable("a claim may not lower an already-earned total");
        }
        if (total == previous) {
            // A no-op claim is not an error: a relayer may submit one from a stale cache, and
            // reverting would waste the caller's gas to change nothing. Emitting nothing keeps
            // the event stream honest about what actually changed.
            return;
        }
        _points[agentId] = total;
        emit PointsClaimed(agentId, previous, total);
    }

    /// @notice ERC-5192: a whole collection that cannot move is locked as a whole.
    ///
    /// @dev This is a DECLARATION and enforces nothing on its own — see the contract comment.
    /// The enforcement is the `_update` override below, and a test asserts a real transfer
    /// reverts rather than trusting this flag.
    function locked(uint256 tokenId) external view override returns (bool) {
        // Reverting for a nonexistent token rather than returning true: a flag read on a
        // token that does not exist would be answering a question about nothing.
        _requireOwned(tokenId);
        return true;
    }

    /// @notice ERC-5192: every token in this collection is locked.
    ///
    /// @dev IERC5192 does not declare `supportsInterface`, so ERC721 is the only parent in the
    /// override list. Reporting the ERC-5192 interface id is still required, because a wallet
    /// checks for it before deciding the token is soulbound — omitting it would make the badge
    /// look transferable to tooling that asks.
    function supportsInterface(bytes4 interfaceId) public view override returns (bool) {
        return interfaceId == type(IERC5192).interfaceId || super.supportsInterface(interfaceId);
    }

    /// @dev The actual soulbound enforcement.
    ///
    /// @dev Both mints and burns must stay legal, and that is the whole subtlety of this
    /// override. A mint moves from address(0); a burn moves to address(0). Everything else is
    /// a transfer between live addresses and must revert. Writing this as "revert unless to or
    /// from is zero" is what makes the check complete; a check that only looked at `to` would
    /// still allow a burn that destroys a badge, and one that only looked at `from` would block
    /// minting entirely.
    function _update(address to, uint256 tokenId, address auth) internal override returns (address) {
        address from = _ownerOf(tokenId);
        // A mint has no previous owner; a burn has no new owner. Any other combination is a
        // transfer, and this collection does not permit one.
        if (from != address(0) && to != address(0)) {
            revert SoulboundNonTransferable();
        }
        return super._update(to, tokenId, auth);
    }

    /// @dev Derives an agent id the same way the Go side does:
    ///
    ///     agentId = keccak256(chainId ‖ address)
    ///
    /// @dev This MUST stay byte-identical to `agentid`/`receipt` in Go, and the two are checked
    /// against shared vectors in CI (invariant A4). An agent the contract calls X and the
    /// client calls Y would make a proof unverifiable for reasons nobody could see.
    function _agentId(uint64 chainId, address owner) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(chainId, owner));
    }

    /// @dev The Merkle leaf for a points claim, mirroring internal/merkle.
    ///
    /// @dev The epoch is inside the leaf. Without it, a proof for epoch 10 would also verify
    /// against epoch 20's root if the same agent and total happened to appear, which would let
    /// a stale claim be replayed into a newer epoch.
    function _leaf(bytes32 agentId, uint256 total, uint64 epoch) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(agentId, total, epoch));
    }
}

/// @dev The minimal slice of RelayAnchor this contract needs.
///
/// @dev Declared here rather than importing the whole contract so the two stay independently
/// deployable, and so RelayPoints does not inherit RelayAnchor's storage layout. An interface
/// is the correct unit of coupling: this contract needs one function, and saying so keeps the
/// dependency honest.
interface RelayRootSource {
    /// @notice Whether a leaf is included in an epoch's published root.
    ///
    /// @dev The signature mirrors RelayAnchor.verifyProof exactly. It is duplicated here rather
    /// than imported so this contract does not depend on RelayAnchor's full source, and a test
    /// pins the two against each other so the copies cannot drift silently.
    function verifyProof(
        address operator,
        uint256 epoch,
        bytes32 leaf,
        uint256 index,
        bytes32[] calldata siblings
    ) external view returns (bool);
}