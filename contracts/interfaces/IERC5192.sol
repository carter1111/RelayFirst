// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title IERC5192 — Minimal Soulbound Token interface
///
/// @notice The ERC-5192 surface, as specified. It is only two functions, and vendoring it is
/// deliberate rather than importing it from the collection implementation: this interface is a
/// standard other people's tooling reads, so it should be the standard's shape and nothing else.
///
/// @dev What this interface can and cannot do, stated because the distinction is the whole
/// reason `RelayPoints` overrides a transfer function:
///
///   - `locked()` is a DECLARATION. It reports that a token should not be transferable, and it
///     enforces nothing. A marketplace that ignores the flag can still move the token.
///   - The `Locked` event announces the lock, and is likewise informational.
///
/// So a contract that implements only this interface has a soulbound token in name. The
/// enforcement has to be a transfer override, and a test must attempt a real transfer rather
/// than read the flag — otherwise the test proves the declaration was written, not that the
/// token cannot move.
interface IERC5192 {
    /// @notice Emitted when the locking status is changed to locked.
    /// @dev Must be emitted when the token is minted, since a soulbound token is locked from
    /// the moment it exists rather than locked later.
    event Locked(uint256 tokenId);

    /// @notice Emitted when the locking status is changed to unlocked.
    /// @dev Present for interface conformance. A collection that is entirely soulbound never
    /// emits it.
    event Unlocked(uint256 tokenId);

    /// @notice Returns the locking status of a token.
    /// @param tokenId The token to query.
    /// @return True when the token is non-transferable.
    function locked(uint256 tokenId) external view returns (bool);
}