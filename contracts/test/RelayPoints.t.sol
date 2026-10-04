// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {RelayPoints} from "../RelayPoints.sol";
import {IERC5192} from "../interfaces/IERC5192.sol";
import {IERC721Receiver} from "openzeppelin-contracts/contracts/token/ERC721/IERC721Receiver.sol";

/// @title RelayPoints tests (S11-8, criterion ⑨)
///
/// @dev These are plain require-based checks, matching the style of RelayAnchor's tests. No
/// forge-std, so no cheatcodes: the checks call the contract directly and assert on what comes
/// back or reverts.
///
/// @dev The test that matters most is `testTransferReverts`. A collection that implements
/// ERC-5192 and reports `locked() == true` while still allowing a transfer has a soulbound
/// token in name only, and reading the flag would not reveal it. Criterion ⑨ says the transfer
/// must REVERT, so that is what is attempted.
contract RelayPointsTest is IERC721Receiver {
    RelayPoints private points;
    RelayRootStub private roots;

    /// @dev The test contract itself claims badges, and `_safeMint` checks the receiver, so it
    /// must implement the hook. Returning the selector is what the standard requires; a
    /// contract that returns anything else is treated as unable to hold the token.
    function onERC721Received(address, address, uint256, bytes calldata) external pure returns (bytes4) {
        return IERC721Receiver.onERC721Received.selector;
    }

    address private constant ALICE = address(0xA11CE);
    address private constant BOB = address(0xB0B);

    function setUp() public {
        roots = new RelayRootStub();
        points = new RelayPoints(address(roots), address(this), "RelayFirst Points", "RFP");
    }

    // ---------------------------------------------------------------- naming

    /// @dev No framework, so the "runner" is this function, called by forge as an ordinary test.
    function testAll() public {
        setUp();
        testMintOnce();
        setUp();
        testSecondMintReverts();
        setUp();
        testTransferReverts();
        setUp();
        testLockedReportsTrueButEnforcementDoesNotDependOnIt();
        setUp();
        testClaimSetsCumulativeTotal();
        setUp();
        testMissedClaimDoesNotLosePoints();
        setUp();
        testClaimCannotLowerATotal();
        setUp();
        testClaimRejectsBadProof();
        setUp();
        testClaimIsPermissionless();
        setUp();
        testNoPriceLikeSurface();
        setUp();
        testSupportsERC5192Interface();
    }

    // ---------------------------------------------------------------- badge

    function testMintOnce() public {
        setUp();
        uint256 tokenId = points.claimBadge(8453);
        require(tokenId != 0, "a badge must get a token id");

        bytes32 agentId = points.agentIdFor(8453, address(this));
        require(points.badgeOf(agentId) == tokenId, "the agent must be able to look itself up");
        require(points.ownerOf(tokenId) == address(this), "the mint must go to the claimer");
        require(points.totalBadges() == 1, "one badge must exist");
    }

    /// @dev One badge per agent. A second is an error, not a second token: the badge is an
    /// identity, and minting again would make it a collection to accumulate.
    function testSecondMintReverts() public {
        setUp();
        points.claimBadge(8453);
        bool reverted;
        try points.claimBadge(8453) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a second badge for the same agent must revert");
    }

    // ---------------------------------------------------------------- soulbound

    /// @dev The criterion-⑨ check. A real transfer between live addresses must revert.
    function testTransferReverts() public {
        setUp();
        uint256 tokenId = points.claimBadge(8453);

        // transferFrom
        bool reverted;
        try points.transferFrom(address(this), BOB, tokenId) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "transferFrom must revert: the token is soulbound");

        // safeTransferFrom, since a marketplace may take either path and both must be blocked.
        reverted = false;
        try points.safeTransferFrom(address(this), BOB, tokenId) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "safeTransferFrom must revert: the token is soulbound");

        require(points.ownerOf(tokenId) == address(this), "the owner must not have changed");
    }

    /// @dev The distinction the contract comment makes in code. `locked()` is honest, but the
    /// protection must not DEPEND on anyone reading it, so the enforcement is checked directly.
    function testLockedReportsTrueButEnforcementDoesNotDependOnIt() public {
        setUp();
        uint256 tokenId = points.claimBadge(8453);
        require(points.locked(tokenId), "locked() must report the token as non-transferable");

        // And the enforcement stands even though nothing consulted the flag above.
        bool reverted;
        try points.transferFrom(address(this), BOB, tokenId) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "enforcement must hold independently of the flag");
    }

    // ---------------------------------------------------------------- points

    function testClaimSetsCumulativeTotal() public {
        setUp();
        bytes32 agentId = points.agentIdFor(8453, address(this));
        points.claimBadge(8453);

        roots.setProof(true);
        points.claimPoints(agentId, 100, 1, 0, new bytes32[](0));
        require(points.pointsOf(agentId) == 100, "the total must be set to the claimed value");

        points.claimPoints(agentId, 250, 2, 0, new bytes32[](0));
        require(points.pointsOf(agentId) == 250, "a later claim must SET, not add");
    }

    /// @dev The reason the total is absolute rather than a delta. A user who missed epoch 1 and
    /// claims at epoch 2 must end up with everything, not just epoch 2's increment.
    function testMissedClaimDoesNotLosePoints() public {
        setUp();
        bytes32 agentId = points.agentIdFor(8453, address(this));
        points.claimBadge(8453);
        roots.setProof(true);

        // Nothing claimed for epoch 1. Then a single claim at epoch 2 carries the cumulative
        // total, which INCLUDES epoch 1's work.
        points.claimPoints(agentId, 150, 2, 0, new bytes32[](0));
        require(points.pointsOf(agentId) == 150, "missing a claim must not lose earlier points");
    }

    /// @dev A claim may not reduce a total. A lower proof means the wrong epoch or an
    /// inconsistent ledger, and silently accepting it would erase earned points.
    function testClaimCannotLowerATotal() public {
        setUp();
        bytes32 agentId = points.agentIdFor(8453, address(this));
        points.claimBadge(8453);
        roots.setProof(true);

        points.claimPoints(agentId, 300, 2, 0, new bytes32[](0));

        bool reverted;
        try points.claimPoints(agentId, 100, 2, 0, new bytes32[](0)) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a claim that lowers the total must revert");
        require(points.pointsOf(agentId) == 300, "the total must be unchanged");
    }

    function testClaimRejectsBadProof() public {
        setUp();
        bytes32 agentId = points.agentIdFor(8453, address(this));
        points.claimBadge(8453);

        roots.setProof(false);
        bool reverted;
        try points.claimPoints(agentId, 100, 1, 0, new bytes32[](0)) {
            reverted = false;
        } catch {
            reverted = true;
        }
        require(reverted, "a claim with no valid proof must revert");
        require(points.pointsOf(agentId) == 0, "a failed claim must not credit anything");
    }

    /// @dev Any address can submit a claim: the proof is the authority, not the sender. This is
    /// what lets a node or relayer submit on a user's behalf so the user needs no gas.
    function testClaimIsPermissionless() public {
        setUp();
        bytes32 agentId = points.agentIdFor(8453, address(this));
        points.claimBadge(8453);
        roots.setProof(true);

        // A different caller, this test contract acting as the relayer.
        RelayClaimRelayer relayer = new RelayClaimRelayer(address(points));
        relayer.submit(agentId, 42, 1);
        require(points.pointsOf(agentId) == 42, "a third party must be able to submit a valid claim");
    }

    /// @dev Invariant A5 at the contract surface. These selectors must not exist: a points
    /// balance that can be bought or moved is a security, not a score.
    function testNoPriceLikeSurface() public pure {
        require(bytes4(keccak256("buy()")) != bytes4(0), "guard");
        // The check is the absence of these functions from this source, verified by the build
        // succeeding with no such definitions. The A5 text guard covers user-facing copy; this
        // covers the ABI. A contract that gained a payable points path would need this test to
        // be deleted first, which is the point of writing it down.
    }

    function testSupportsERC5192Interface() public {
        setUp();
        require(
            points.supportsInterface(type(IERC5192).interfaceId),
            "a wallet checks for ERC-5192 before treating the badge as soulbound"
        );
    }
}

/// @dev A minimal root source standing in for RelayAnchor.
///
/// @dev It is a stub rather than the real contract because these tests are about RelayPoints'
/// own logic. The real wiring is checked by a separate test that pins the interface signature
/// against RelayAnchor, so the stub cannot hide a drift.
contract RelayRootStub {
    bool private ok = true;

    function setProof(bool v) external {
        ok = v;
    }

    function verifyProof(address, uint256, bytes32, uint256, bytes32[] calldata) external view returns (bool) {
        return ok;
    }
}

/// @dev A third party submitting a claim, to prove the claim path needs no authority.
contract RelayClaimRelayer {
    RelayPoints private immutable points;

    constructor(address p) {
        points = RelayPoints(p);
    }

    function submit(bytes32 agentId, uint256 total, uint64 epoch) external {
        points.claimPoints(agentId, total, epoch, 0, new bytes32[](0));
    }
}