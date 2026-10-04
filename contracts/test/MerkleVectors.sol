// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title MerkleVectors — GENERATED FILE, DO NOT EDIT BY HAND.
///
/// @notice The same vectors as testdata/merkle-vectors.json, emitted by
///         internal/devtools/gen_merkle_vectors.go so both languages are compared
///         against data that provably came from one computation.
///
/// @dev Regenerate with:
///        go run internal/devtools/gen_merkle_vectors.go
///      CI fails if this file is out of date.
library MerkleVectors {
    // ---- n=1: 1 leaf/leaves, width 1, 1 proof(s) ----
    function _tree0() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](1);
        leaves[0] = 0x6dfe4a8998be0f9f1a4c76457b8592ba6f16f0929d5e96c5800cc583417eea03;
        width = 1;
        root = 0x9015613ba217d255915a7e48f7efe76ddd8ff892eb84c41c0f4aaae64348e63e;
        indices = new uint256[](1);
        siblings = new bytes32[][](1);
        indices[0] = 0;
        siblings[0] = new bytes32[](0);
    }

    // ---- n=2: 2 leaf/leaves, width 2, 2 proof(s) ----
    function _tree1() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](2);
        leaves[0] = 0x585eb341386723c6eca44d123d419dba91daa686c39269e5e7ccdee5ee7a51e5;
        leaves[1] = 0xfb5af303148aafe2a9c08f79b9e56ad6f840feecbae42316889a66017b4b8f65;
        width = 2;
        root = 0xdff2d3fb52779446f848fb725d2eadc88f8a1699395ff326f5a2a5d89876cd02;
        indices = new uint256[](2);
        siblings = new bytes32[][](2);
        indices[0] = 0;
        siblings[0] = new bytes32[](1);
        siblings[0][0] = 0x4d584ce9806b9c0a997ae3bffef385cec8d9afd56edb6f7fce795f4f79d8aad6;
        indices[1] = 1;
        siblings[1] = new bytes32[](1);
        siblings[1][0] = 0x3d2c3314d63c7ab88e3d20c049ed4364d89a7bc62ff4e45fb48a94dd592e6c5d;
    }

    // ---- n=3: 3 leaf/leaves, width 4, 3 proof(s) ----
    function _tree2() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](3);
        leaves[0] = 0xa67563c5560ec003872bd929c409bf5f8edb320edfd3f77e9b41c4276eeb6908;
        leaves[1] = 0x9a2c9010e06afd0c12fb0626e0dd760d75b7c2869f7337f1d4f17bdda905b6a2;
        leaves[2] = 0xc34253515e4979ba1d96d178bc0359d31f802906a6ccc6066acae6c6b7653215;
        width = 4;
        root = 0x859590649d224f2eb0edbd735c0df64a97239893683f88c20522428622f1b27c;
        indices = new uint256[](3);
        siblings = new bytes32[][](3);
        indices[0] = 0;
        siblings[0] = new bytes32[](2);
        siblings[0][0] = 0x8a09dd725f0c8cf393ac41449d837398d916771ddb7163fe0f4b57692df50a12;
        siblings[0][1] = 0xca9265bc5ece67214045db07bcb699045105a3fb61fea08afbf75d5ee5ec7805;
        indices[1] = 1;
        siblings[1] = new bytes32[](2);
        siblings[1][0] = 0x4db4447fc3b54850ebf02039896fc65d368e3cd378a61463e4d8d11aa33e5be2;
        siblings[1][1] = 0xca9265bc5ece67214045db07bcb699045105a3fb61fea08afbf75d5ee5ec7805;
        indices[2] = 2;
        siblings[2] = new bytes32[](2);
        siblings[2][0] = 0xf39a869f62e75cf5f0bf914688a6b289caf2049435d8e68c5c5e6d05e44913f3;
        siblings[2][1] = 0x564938cafba4eca8dac2de632a61b5f7db82ac4b807bf6c342dedd0f53cc61be;
    }

    // ---- n=4: 4 leaf/leaves, width 4, 4 proof(s) ----
    function _tree3() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](4);
        leaves[0] = 0x88610657abc82399cb867633e02883af86245c26bcc63f289ba240aca0e5d166;
        leaves[1] = 0x0866aba8b6d90a1ef15f73b4f53f032f55eed7267a66020fe5d5b438172b03a8;
        leaves[2] = 0x0bbcb44db15ec6026c30610397ce7c208336f8a327e733ee59cf8cdce330a17b;
        leaves[3] = 0x6e6aa96d6c865a89308e46f12b636469fdda82411f0b20e5274befa1590187f6;
        width = 4;
        root = 0xbaa5586cf38471bd6a41442b95dd57403149f16f1d376ba73383e490bb60ff22;
        indices = new uint256[](4);
        siblings = new bytes32[][](4);
        indices[0] = 0;
        siblings[0] = new bytes32[](2);
        siblings[0][0] = 0xda436388405aaacc30837d6afca4abfd0d7bfc442ffcfb6d5badec83fbc78912;
        siblings[0][1] = 0x0e1d33887d650ce8958ce08896bc6393187903bc221ca3da57ea8b284e9ec028;
        indices[1] = 1;
        siblings[1] = new bytes32[](2);
        siblings[1][0] = 0xcde1ac0e4188841652811ccbcfd6a868573b6a35c0ffb5d946af90e44b4bb51c;
        siblings[1][1] = 0x0e1d33887d650ce8958ce08896bc6393187903bc221ca3da57ea8b284e9ec028;
        indices[2] = 2;
        siblings[2] = new bytes32[](2);
        siblings[2][0] = 0xb37172970b368d664ca17ec999950e52d87c718c851bfea14bd7a8bdcf8843c7;
        siblings[2][1] = 0x8d0cd7e505b580737c7b4e08eca5d33f9648c56895c59d24f679513ecb16692d;
        indices[3] = 3;
        siblings[3] = new bytes32[](2);
        siblings[3][0] = 0x15ed4bb6910ccf5c297f0358db51f0838598cb5262f328aac733c810a12bb1cf;
        siblings[3][1] = 0x8d0cd7e505b580737c7b4e08eca5d33f9648c56895c59d24f679513ecb16692d;
    }

    // ---- n=5: 5 leaf/leaves, width 8, 5 proof(s) ----
    function _tree4() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](5);
        leaves[0] = 0x644023963507a515e13690e63c5495e2f17eb5876ba6bc09ee5aa5fe5cfa8ad9;
        leaves[1] = 0xfd3583a6ab412d86008970d046b46dc0c3cd2d9f2728f4ed99933d364705d0a0;
        leaves[2] = 0x5fd69b132c84bede64917876bbb6e6697fa01ccb1679fc6b69ff637021b947eb;
        leaves[3] = 0xfe0b3fb3f865b7e90f9fb37514aedbdc0579b5c9e0b43523341c394ef55a2db2;
        leaves[4] = 0x39e4607055f44a7cc03e5ab823b97f5c84980bab5bf37cd6269aea7333346f57;
        width = 8;
        root = 0x81ca64c03366563f593b06aa3f8459454634513f0a8adadce2807d20824e9967;
        indices = new uint256[](5);
        siblings = new bytes32[][](5);
        indices[0] = 0;
        siblings[0] = new bytes32[](3);
        siblings[0][0] = 0x5666aeab209dd698a88be7116b443f2440371d50e202b0c25165f721215e8833;
        siblings[0][1] = 0xe8c722bc7fca79535e3597c9bd9af7cef8bdcb74d5957db09e8b52c81e28e945;
        siblings[0][2] = 0x2f144a5c94189b307997780ffc63e0b3b1073585f4ed9feb0705c72147c509a3;
        indices[1] = 1;
        siblings[1] = new bytes32[](3);
        siblings[1][0] = 0xd09f42a184ee06851795c96108f845ccaff98bdd8d8ad831c5ebe705b85758a0;
        siblings[1][1] = 0xe8c722bc7fca79535e3597c9bd9af7cef8bdcb74d5957db09e8b52c81e28e945;
        siblings[1][2] = 0x2f144a5c94189b307997780ffc63e0b3b1073585f4ed9feb0705c72147c509a3;
        indices[2] = 2;
        siblings[2] = new bytes32[](3);
        siblings[2][0] = 0x18870978b4e7ec7a0f9dd00cc766116640aaf490393f447a846a8edfd7d8310d;
        siblings[2][1] = 0xcc93fd519820cbb6e587f4b78f9744731f92336c8f2793f9cd8d0b03ac443751;
        siblings[2][2] = 0x2f144a5c94189b307997780ffc63e0b3b1073585f4ed9feb0705c72147c509a3;
        indices[3] = 3;
        siblings[3] = new bytes32[](3);
        siblings[3][0] = 0xbeb929ff52718c2edf02ad101f14f015dc8e922d6da4fe2db6cf72819d33c9f0;
        siblings[3][1] = 0xcc93fd519820cbb6e587f4b78f9744731f92336c8f2793f9cd8d0b03ac443751;
        siblings[3][2] = 0x2f144a5c94189b307997780ffc63e0b3b1073585f4ed9feb0705c72147c509a3;
        indices[4] = 4;
        siblings[4] = new bytes32[](3);
        siblings[4][0] = 0xf39a869f62e75cf5f0bf914688a6b289caf2049435d8e68c5c5e6d05e44913f3;
        siblings[4][1] = 0x4ed5c02d6d48c8932486c99d3ad999e5d8949dc3be3b3058cc2979690c3e3a62;
        siblings[4][2] = 0xdad13b4fe78aa3f9c44fa172784c270722d606e1efa55f4a3241daf71c1bc78e;
    }

    // ---- n=7: 7 leaf/leaves, width 8, 7 proof(s) ----
    function _tree5() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](7);
        leaves[0] = 0xae54574ca9bf3ae85c5cb8b1a690b616690411e1ec7742c8e5b73121ea8d3079;
        leaves[1] = 0x0778eaff27ce9ac582129c161edadd1d09c27fddc1aadac42288e1ef836cdf31;
        leaves[2] = 0x8df1c8f80ed709a03a5cab11de87c97cbe958daede646da84d38855ab45ad292;
        leaves[3] = 0x2c87e4529bfced45b7df4cf198b85f11c166354d8777ef2b595c2ec348122f92;
        leaves[4] = 0xfccb2ffe2c384810564066a737fcb7af69caede6f4b852c97d8a9231ebe4e1b3;
        leaves[5] = 0x36deb62d322143423eb734f50257387994f8016d984afe9c3430cb0dd2f501df;
        leaves[6] = 0x7d8204a44b695131aca53b6e9fb2da7f2b7eb6b9a8d65e93f133a47651be42ba;
        width = 8;
        root = 0x0e96ea9eb0e78cf8fbdf782ccf0aaaddda8819d884e1b2c0d2331208b7db1a64;
        indices = new uint256[](7);
        siblings = new bytes32[][](7);
        indices[0] = 0;
        siblings[0] = new bytes32[](3);
        siblings[0][0] = 0x6868ffdeff0c3d817333f946c6cfbff5feb526e52d5e98d61916972b7cd441cf;
        siblings[0][1] = 0x199844ac79574e54ad04754d21af4dc2c2d95847e1b44cc0a522d359331d3bdf;
        siblings[0][2] = 0xf0a9e42756f0eb70e6280b8c2b48af2b10a238bac58500c1edb05493559cddec;
        indices[1] = 1;
        siblings[1] = new bytes32[](3);
        siblings[1][0] = 0x94c178f53860fd5bd88cda4eaffbe22f258c853ba38a16baf45dfe6b19795b50;
        siblings[1][1] = 0x199844ac79574e54ad04754d21af4dc2c2d95847e1b44cc0a522d359331d3bdf;
        siblings[1][2] = 0xf0a9e42756f0eb70e6280b8c2b48af2b10a238bac58500c1edb05493559cddec;
        indices[2] = 2;
        siblings[2] = new bytes32[](3);
        siblings[2][0] = 0xd90303cf430539dbce5643eafe73108e5e6bca9aafa2eacdcc89e0c67f2fd7a1;
        siblings[2][1] = 0xab7731195d699f60c56503be57ac9daedf68eb58c4a68e55aca6049b466b1653;
        siblings[2][2] = 0xf0a9e42756f0eb70e6280b8c2b48af2b10a238bac58500c1edb05493559cddec;
        indices[3] = 3;
        siblings[3] = new bytes32[](3);
        siblings[3][0] = 0xce2739d31d1d380a89c16f8241a1121712c9ece67aab85ea06e687b1744afaac;
        siblings[3][1] = 0xab7731195d699f60c56503be57ac9daedf68eb58c4a68e55aca6049b466b1653;
        siblings[3][2] = 0xf0a9e42756f0eb70e6280b8c2b48af2b10a238bac58500c1edb05493559cddec;
        indices[4] = 4;
        siblings[4] = new bytes32[](3);
        siblings[4][0] = 0x4eb0db8faad65a33548d1362ab522904e6e764353c3d6128b6ad96193027323b;
        siblings[4][1] = 0x02ebaf6343228a776d0042f4510e913b07576194ff0e414056ba895c80d3281b;
        siblings[4][2] = 0x72206a38fb421d4f87672a548f22704dbbc16e4b28037a1159ae9b69d73bde02;
        indices[5] = 5;
        siblings[5] = new bytes32[](3);
        siblings[5][0] = 0x2a03b90d2efbe210bb934de1d24045e3b90c8f0514e4f03ff04f3d927971e0d5;
        siblings[5][1] = 0x02ebaf6343228a776d0042f4510e913b07576194ff0e414056ba895c80d3281b;
        siblings[5][2] = 0x72206a38fb421d4f87672a548f22704dbbc16e4b28037a1159ae9b69d73bde02;
        indices[6] = 6;
        siblings[6] = new bytes32[](3);
        siblings[6][0] = 0xf39a869f62e75cf5f0bf914688a6b289caf2049435d8e68c5c5e6d05e44913f3;
        siblings[6][1] = 0x929601fb0d1faf51e8533560e48f23a999cbb476f24111d3e172d300c25ea592;
        siblings[6][2] = 0x72206a38fb421d4f87672a548f22704dbbc16e4b28037a1159ae9b69d73bde02;
    }

    // ---- n=8: 8 leaf/leaves, width 8, 8 proof(s) ----
    function _tree6() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](8);
        leaves[0] = 0x8d2c714a9550ffc424c097267d70e53408cb45ab2d4ab470bd2a713d2453900b;
        leaves[1] = 0x72fceaacbc334a977a3c3fdeac2d46c84f6b2506e3760dd9c79c9e89d4788c3f;
        leaves[2] = 0x1967e80462a22e1a38938ba3608417ced5cd809cc649a1c752508701043b75df;
        leaves[3] = 0x5286283efe62dc14b7ff29c018c766933ee27375aae30b1ae7f9a3b2ab5cb75d;
        leaves[4] = 0x7c9a21d82631ede4e68f0128f8eb8621b15d46c1e8fdea1c0eff09ba8d3104aa;
        leaves[5] = 0xfdc13edca1429a062abaf2746892371bb9d9b420f22453053e9e60231c970014;
        leaves[6] = 0x70ebb212ecfbadd21b84271df0741912e847f67f2eee80afb5d8305380cedb93;
        leaves[7] = 0x29f5696e2d2de4fa8ebe7169b93c6899de926d09ddb6d944a4238510dfb870c0;
        width = 8;
        root = 0xa3f6a6c70b27fba1e74a9c81e5b870d27545017cf2ff648ed83e45df9926e1d1;
        indices = new uint256[](8);
        siblings = new bytes32[][](8);
        indices[0] = 0;
        siblings[0] = new bytes32[](3);
        siblings[0][0] = 0xb4bb4befe9cbc63789500d8b9c8f73f6a7e6b7a2f1ba5e3d5fa833befb6369ad;
        siblings[0][1] = 0x22f13c382bcefb8cc14b68233cb643898c6add1202766b6d80710093c2629c65;
        siblings[0][2] = 0x798eb5a165bbbd4cb50619c51689b871f432e1190f9d2666a40487fdfdc1421e;
        indices[1] = 1;
        siblings[1] = new bytes32[](3);
        siblings[1][0] = 0xfa3b43227f92997b1d786b148b0e39c7127dd24ac62b3be6351f8d8c1e63cbd7;
        siblings[1][1] = 0x22f13c382bcefb8cc14b68233cb643898c6add1202766b6d80710093c2629c65;
        siblings[1][2] = 0x798eb5a165bbbd4cb50619c51689b871f432e1190f9d2666a40487fdfdc1421e;
        indices[2] = 2;
        siblings[2] = new bytes32[](3);
        siblings[2][0] = 0x8581415cad7a8e4f69e6f4a3eafad4d4fc6c5e537a577d11172273a3636f788e;
        siblings[2][1] = 0x11a42b1528e0f53596b5ff4b3e9de5d7128cc8cbaf10e55ff555ce6bea561e6a;
        siblings[2][2] = 0x798eb5a165bbbd4cb50619c51689b871f432e1190f9d2666a40487fdfdc1421e;
        indices[3] = 3;
        siblings[3] = new bytes32[](3);
        siblings[3][0] = 0x7e4d815e00de2c52c7de0f0d4f63b862d65eea25f8ea59919df4edb831ddb768;
        siblings[3][1] = 0x11a42b1528e0f53596b5ff4b3e9de5d7128cc8cbaf10e55ff555ce6bea561e6a;
        siblings[3][2] = 0x798eb5a165bbbd4cb50619c51689b871f432e1190f9d2666a40487fdfdc1421e;
        indices[4] = 4;
        siblings[4] = new bytes32[](3);
        siblings[4][0] = 0x0369154e32dc8b9fc402337db3b0bff9113b092bbc179ca9078626a4a1313dba;
        siblings[4][1] = 0xcbaef70868e613dfaed8164cb50db99754f1354fab72652a8b941a54be10be99;
        siblings[4][2] = 0x7ef4b6f6e53fc41de7935bb71b7cee28d1acf4acd567a38c25df112d0f90804a;
        indices[5] = 5;
        siblings[5] = new bytes32[](3);
        siblings[5][0] = 0xb0ac9e3d2fa95f4c776ed1bfa7498075c653c1fc36539ba75588c16055083aa1;
        siblings[5][1] = 0xcbaef70868e613dfaed8164cb50db99754f1354fab72652a8b941a54be10be99;
        siblings[5][2] = 0x7ef4b6f6e53fc41de7935bb71b7cee28d1acf4acd567a38c25df112d0f90804a;
        indices[6] = 6;
        siblings[6] = new bytes32[](3);
        siblings[6][0] = 0x6da51be3ee10e4d68e7a3aa2777784df3064a91100870d922cc4d251db03156e;
        siblings[6][1] = 0xe28ccd4e1b796b174e1cd4fb09c01b8bc3c3d9dc2057b5908caceab19e1e68a8;
        siblings[6][2] = 0x7ef4b6f6e53fc41de7935bb71b7cee28d1acf4acd567a38c25df112d0f90804a;
        indices[7] = 7;
        siblings[7] = new bytes32[](3);
        siblings[7][0] = 0xe77a2c013a5026220d1c7e0028a7b40c322e46c96d791d31cad19fc6cd7cc31e;
        siblings[7][1] = 0xe28ccd4e1b796b174e1cd4fb09c01b8bc3c3d9dc2057b5908caceab19e1e68a8;
        siblings[7][2] = 0x7ef4b6f6e53fc41de7935bb71b7cee28d1acf4acd567a38c25df112d0f90804a;
    }

    // ---- n=9: 9 leaf/leaves, width 16, 9 proof(s) ----
    function _tree7() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](9);
        leaves[0] = 0x5d45f0b8a9fb1e263d92f218941f0408c6355386bcc61c7f055d7fa8f910e635;
        leaves[1] = 0x211d6c5960f4cae4c36f718e2e50309a6c78a452f9cdf46f4d975938743e5100;
        leaves[2] = 0x71812f78fffa8dc0139be9772fcb35b2565b5ce2a039bed99ff94982056dc212;
        leaves[3] = 0xd0fdc1c14b96a42211324c70d57fe9d294d7ee03c3716a7b01f1a46900e5fd41;
        leaves[4] = 0x00a85d4b4759d34b04fa5996cf769f3388096747df23b50916c9dd64bb83f01c;
        leaves[5] = 0xab0217b51faa466cdc4e9764e68989d3de9508a361c13af15a53ae0aeecd3c6f;
        leaves[6] = 0xf392df57e00e59b0e32d4e21330c7d7017dd2ff5955b73a77a22f1673def1eec;
        leaves[7] = 0x983bda9c787283161e39f227c7135dbec36bf68280be01b86303cc5b52a2119d;
        leaves[8] = 0xc1553aa060c1e7b578d0662788f4cc967d9379d08ccd06646bcf66fe6a4925c5;
        width = 16;
        root = 0xc35ff5fcc97832487e5183ba17b318bd1df3396effbd95bf6ae6d24ace982def;
        indices = new uint256[](9);
        siblings = new bytes32[][](9);
        indices[0] = 0;
        siblings[0] = new bytes32[](4);
        siblings[0][0] = 0x0755dc9b0561625c4f5b04d615d6021d673758778ba9c7b3bd363f9a0a9bb8c4;
        siblings[0][1] = 0x95981324cd4f0d7bf2b600dd4e14ef670dd5d1e2b2503e74b9defe6860f48b5a;
        siblings[0][2] = 0xe318b223479f5f6a67271e28b6053e1665afeb571291b7cfc931d5bfda695be6;
        siblings[0][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[1] = 1;
        siblings[1] = new bytes32[](4);
        siblings[1][0] = 0x8cffc1a988956c9f31a6b1087edb3b0d2bbf12444f8b04721219cbbc4366e582;
        siblings[1][1] = 0x95981324cd4f0d7bf2b600dd4e14ef670dd5d1e2b2503e74b9defe6860f48b5a;
        siblings[1][2] = 0xe318b223479f5f6a67271e28b6053e1665afeb571291b7cfc931d5bfda695be6;
        siblings[1][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[2] = 2;
        siblings[2] = new bytes32[](4);
        siblings[2][0] = 0x7fd4cbd4a2ec80e75d5bd1121a12df56ceb3289816000658190d3e213d37700a;
        siblings[2][1] = 0xee8700220ed526e8e797054c7025202517ca6322031378525bf2179f3b98d336;
        siblings[2][2] = 0xe318b223479f5f6a67271e28b6053e1665afeb571291b7cfc931d5bfda695be6;
        siblings[2][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[3] = 3;
        siblings[3] = new bytes32[](4);
        siblings[3][0] = 0x97665b4bf736517e2f3d5ef5bf3ef57e10e56ed317fcbb25b83c16f7b793d05e;
        siblings[3][1] = 0xee8700220ed526e8e797054c7025202517ca6322031378525bf2179f3b98d336;
        siblings[3][2] = 0xe318b223479f5f6a67271e28b6053e1665afeb571291b7cfc931d5bfda695be6;
        siblings[3][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[4] = 4;
        siblings[4] = new bytes32[](4);
        siblings[4][0] = 0x2a33a550d5ea6ebf1d3266fd58124bd7c0647d833555fa11e2aa94261fb48bcd;
        siblings[4][1] = 0x83a72c5aea7c68949dc905b77423ea2f158436e22846a74b103a9f0a26407e54;
        siblings[4][2] = 0xdc2cd2f87441c36b886ddd3c105c3ad456cc2d67d73d848fd889e7430019c628;
        siblings[4][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[5] = 5;
        siblings[5] = new bytes32[](4);
        siblings[5][0] = 0xd543781ca75665812de2506874e13fe5d7f75d11fe08235b0ee7341f65f98cf0;
        siblings[5][1] = 0x83a72c5aea7c68949dc905b77423ea2f158436e22846a74b103a9f0a26407e54;
        siblings[5][2] = 0xdc2cd2f87441c36b886ddd3c105c3ad456cc2d67d73d848fd889e7430019c628;
        siblings[5][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[6] = 6;
        siblings[6] = new bytes32[](4);
        siblings[6][0] = 0xb8ca1383d1d565f78bebc28966c29f8fa81af4f7e2df1613e80fcc4cbea8a782;
        siblings[6][1] = 0x63eb27b7ca7e1a6f245f9126fd87503331cfde525f641b31e92adcf0a4063c1d;
        siblings[6][2] = 0xdc2cd2f87441c36b886ddd3c105c3ad456cc2d67d73d848fd889e7430019c628;
        siblings[6][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[7] = 7;
        siblings[7] = new bytes32[](4);
        siblings[7][0] = 0xf219a521ac17c805e496c00a962398b935e789b7f98fe7acf631834d91baab5a;
        siblings[7][1] = 0x63eb27b7ca7e1a6f245f9126fd87503331cfde525f641b31e92adcf0a4063c1d;
        siblings[7][2] = 0xdc2cd2f87441c36b886ddd3c105c3ad456cc2d67d73d848fd889e7430019c628;
        siblings[7][3] = 0x6c1c6c26eb7cafa20ee669ac23a91b69fa64f7e0bba03db0c7cf3045564fa0cc;
        indices[8] = 8;
        siblings[8] = new bytes32[](4);
        siblings[8][0] = 0xf39a869f62e75cf5f0bf914688a6b289caf2049435d8e68c5c5e6d05e44913f3;
        siblings[8][1] = 0x4ed5c02d6d48c8932486c99d3ad999e5d8949dc3be3b3058cc2979690c3e3a62;
        siblings[8][2] = 0x1c792b14bf66f82af36f00f5fba7014fa0c1e2ff3c7c273bfe523c1acf67dc3f;
        siblings[8][3] = 0xf2dffaeb520b4ed1bdf0c73a73fd112ddbcca8d570a78ad9e3fe83aa93f9c668;
    }

    // ---- n=16: 16 leaf/leaves, width 16, 16 proof(s) ----
    function _tree8() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](16);
        leaves[0] = 0x47d257dc1ae8ddc6043b2e1f8413b62bef2cfd54ffdc2d0be7549ead5bb032eb;
        leaves[1] = 0x0ad0c9f1fc1c4fa3efd8eee6416528b32c89eb016bc956cf469988bad4d113e3;
        leaves[2] = 0xe5ebf53f9f040326f97de0b3e0083a7ca10122a51a2d72c9b1bccf837d9be6c5;
        leaves[3] = 0xbded2e57d7ef2ee8dda191f371ff12d252cd14316866b0530b33d2ce1b25c150;
        leaves[4] = 0x815fab2f93487fa97c614bf27db8069db07826eff1a3ce705f855073e1379ba1;
        leaves[5] = 0xd97f123245cddeef61b359b62b970fcb58e1bf8896d1ffcce76fd4d3546b7a2a;
        leaves[6] = 0x36416fcd299e4222b8e7b21acb95bb4a066f4c7257b10f26b2aae583d04174e5;
        leaves[7] = 0xfbf72c793e891ae83858ad3c1ff1ea7f3981a3af8d6d662f64167644b4e5abbd;
        leaves[8] = 0x19ac1122e908ff5204e9ea70fdd2263ea9e9ea75d088d44d69c5176b50db5206;
        leaves[9] = 0x95d96a6ad741f3638545d0191d7f949bc89f0f25168fa1ad7e1ad1aa8e09dd0e;
        leaves[10] = 0x75fa1ae764d3da35eaefa4204338aec69682fca0ff4d17a28e627adc1a965ed4;
        leaves[11] = 0x03a4ce7b2373a79d46cfa25e209df813ca65fbddeae7bd7e3a81a2074a764e4f;
        leaves[12] = 0x8121b6e836244c840f7e14391879ec6a7e4c11f7282ec5be59764095a635687c;
        leaves[13] = 0x725a2efb3c518d4bcfc65275a8fff69cec3950061d07900a3cc867f39f403252;
        leaves[14] = 0xf9d2d56f41650c3af6848a2cd4cc69e1f17fa036eeaef3a83becadfc092a525d;
        leaves[15] = 0xd96e64788304b6c4a950fae81ff671fbb7350bfd1fb5e68a3cf081a51415138f;
        width = 16;
        root = 0x593c4d8eef22fc178f6f981cafb894f7912f9e5093d38cfdf67fa8a67b5ec16d;
        indices = new uint256[](16);
        siblings = new bytes32[][](16);
        indices[0] = 0;
        siblings[0] = new bytes32[](4);
        siblings[0][0] = 0xaed81338a8580cd06438d801787cef458608904ef7658fefe0b0289348b972f6;
        siblings[0][1] = 0x4463ccd70e0fac740dc22ec40a39e7ef6a5b337d973530e2d7f7588d39cc0cd0;
        siblings[0][2] = 0xc4355c1652d300f94fe14fe9b8cda03b845b996fd625bd98a1f99d4fc997b42e;
        siblings[0][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[1] = 1;
        siblings[1] = new bytes32[](4);
        siblings[1][0] = 0x85848bdfcbd5daa843fada1c9b6d06eb60c2d32c613abe23dba0acf7b699ced7;
        siblings[1][1] = 0x4463ccd70e0fac740dc22ec40a39e7ef6a5b337d973530e2d7f7588d39cc0cd0;
        siblings[1][2] = 0xc4355c1652d300f94fe14fe9b8cda03b845b996fd625bd98a1f99d4fc997b42e;
        siblings[1][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[2] = 2;
        siblings[2] = new bytes32[](4);
        siblings[2][0] = 0x4e0d0e34c80f689df00b8d4be62f9311f7b00c9b44177a76643e8fdc7e110cd0;
        siblings[2][1] = 0xd145b7da5e1d096a69bc909c6ec4f04bedfd67f1c460aefd00c1094f36667f38;
        siblings[2][2] = 0xc4355c1652d300f94fe14fe9b8cda03b845b996fd625bd98a1f99d4fc997b42e;
        siblings[2][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[3] = 3;
        siblings[3] = new bytes32[](4);
        siblings[3][0] = 0x5626621c287f0679f0d85bd0e8e30379b101515a82fab150ef254adef0e018cd;
        siblings[3][1] = 0xd145b7da5e1d096a69bc909c6ec4f04bedfd67f1c460aefd00c1094f36667f38;
        siblings[3][2] = 0xc4355c1652d300f94fe14fe9b8cda03b845b996fd625bd98a1f99d4fc997b42e;
        siblings[3][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[4] = 4;
        siblings[4] = new bytes32[](4);
        siblings[4][0] = 0x23bb92aaf70197255c154175ebd965fd5195dbb80d1d02a60d6307e0f0a6f49d;
        siblings[4][1] = 0x23921b28ccd836f7bc028efa764ca0dfe6c36d329ee206a1e53d63063e0946c0;
        siblings[4][2] = 0xde20075193f62944de7e209e19b102705c5b528dec2211539b88fd25991e95c0;
        siblings[4][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[5] = 5;
        siblings[5] = new bytes32[](4);
        siblings[5][0] = 0x5218a1f95a40bb5e132c90ade9fca7f9e8d84992ad9e20b0e8375afe35112ccd;
        siblings[5][1] = 0x23921b28ccd836f7bc028efa764ca0dfe6c36d329ee206a1e53d63063e0946c0;
        siblings[5][2] = 0xde20075193f62944de7e209e19b102705c5b528dec2211539b88fd25991e95c0;
        siblings[5][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[6] = 6;
        siblings[6] = new bytes32[](4);
        siblings[6][0] = 0xf43fa676d2d057079e41780d590e4393f8bba7f5dbeaac28b0a15c292b3628de;
        siblings[6][1] = 0xd39ee8541ecd74e23b046e4644ff35ae27ea82ee4244b05d787f742a15e5f561;
        siblings[6][2] = 0xde20075193f62944de7e209e19b102705c5b528dec2211539b88fd25991e95c0;
        siblings[6][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[7] = 7;
        siblings[7] = new bytes32[](4);
        siblings[7][0] = 0xc442ce4f713c2640e0820aaf1a522699f65bc46e2dbf1761215c370c8e7560d0;
        siblings[7][1] = 0xd39ee8541ecd74e23b046e4644ff35ae27ea82ee4244b05d787f742a15e5f561;
        siblings[7][2] = 0xde20075193f62944de7e209e19b102705c5b528dec2211539b88fd25991e95c0;
        siblings[7][3] = 0x18fe79ee498cf013fbc85f4965a1aed9a553d882fa0e29753747e6415c1e47bd;
        indices[8] = 8;
        siblings[8] = new bytes32[](4);
        siblings[8][0] = 0x0122c142e03505e25ca2417de1e22193cc85bcea21ab47fbfcbe2ffc583e4c66;
        siblings[8][1] = 0x950b9cd560d705ab5edd7229172c306b62b36f35b41f7d1674739a6a01cbbaf9;
        siblings[8][2] = 0x3d59167aab7ed391f9040d53905aac1bfb2537d8b2ffed59a2703fde96fd437b;
        siblings[8][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[9] = 9;
        siblings[9] = new bytes32[](4);
        siblings[9][0] = 0x809f31a40192c3a167d97b4f44f55bd8a85fbcccc502d66602a55702e63c29c7;
        siblings[9][1] = 0x950b9cd560d705ab5edd7229172c306b62b36f35b41f7d1674739a6a01cbbaf9;
        siblings[9][2] = 0x3d59167aab7ed391f9040d53905aac1bfb2537d8b2ffed59a2703fde96fd437b;
        siblings[9][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[10] = 10;
        siblings[10] = new bytes32[](4);
        siblings[10][0] = 0xfdb9bbed8574bc4372ce414fb3f4539fa4c7cff11f7a453ef5d1a64c854b7933;
        siblings[10][1] = 0x6b3c5238c2a9522641e4ffe6b8d281f9d7552e711632e50d13f2839b570a8406;
        siblings[10][2] = 0x3d59167aab7ed391f9040d53905aac1bfb2537d8b2ffed59a2703fde96fd437b;
        siblings[10][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[11] = 11;
        siblings[11] = new bytes32[](4);
        siblings[11][0] = 0x8dc0da7da7f786f0cabdb3f574789076968966de67ea1379a3b94ab33a859f6a;
        siblings[11][1] = 0x6b3c5238c2a9522641e4ffe6b8d281f9d7552e711632e50d13f2839b570a8406;
        siblings[11][2] = 0x3d59167aab7ed391f9040d53905aac1bfb2537d8b2ffed59a2703fde96fd437b;
        siblings[11][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[12] = 12;
        siblings[12] = new bytes32[](4);
        siblings[12][0] = 0x807efcf257c928aa4fb423f572b663f38e26b2a8654739e68e43200048314dac;
        siblings[12][1] = 0xa09b04204388b68048050b3e87ac3b334074975bc6265159a49b10ffc7f9f280;
        siblings[12][2] = 0x1035fd26e0505ea7cedb3ffe1479aa899d0d2a45f9c2cbf2e79197513528c6ed;
        siblings[12][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[13] = 13;
        siblings[13] = new bytes32[](4);
        siblings[13][0] = 0x7965d9451c5dfccbf7c98e048077980719fa0b6ee38470655f3d60eb30894421;
        siblings[13][1] = 0xa09b04204388b68048050b3e87ac3b334074975bc6265159a49b10ffc7f9f280;
        siblings[13][2] = 0x1035fd26e0505ea7cedb3ffe1479aa899d0d2a45f9c2cbf2e79197513528c6ed;
        siblings[13][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[14] = 14;
        siblings[14] = new bytes32[](4);
        siblings[14][0] = 0xa1927780a70d7a48f8efbc6c8f801dc41004a9dfc992949a2432f3d5e37a52fe;
        siblings[14][1] = 0xfad3327f08f0273ebce485e24bfbd8c7a0bfb49098465c06d04b9d86b6b1f904;
        siblings[14][2] = 0x1035fd26e0505ea7cedb3ffe1479aa899d0d2a45f9c2cbf2e79197513528c6ed;
        siblings[14][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
        indices[15] = 15;
        siblings[15] = new bytes32[](4);
        siblings[15][0] = 0xc27a3078b9c72793a872b6a25189409c0e4e174d5ef2e0b42f2c44113787797e;
        siblings[15][1] = 0xfad3327f08f0273ebce485e24bfbd8c7a0bfb49098465c06d04b9d86b6b1f904;
        siblings[15][2] = 0x1035fd26e0505ea7cedb3ffe1479aa899d0d2a45f9c2cbf2e79197513528c6ed;
        siblings[15][3] = 0x548e5177e0181ad015c2d821edd2786e23fd0150c098e765b8f5554345f806e7;
    }

    // ---- n=17: 17 leaf/leaves, width 32, 17 proof(s) ----
    function _tree9() private pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        leaves = new bytes32[](17);
        leaves[0] = 0xae0f31736e421031a9165c73c82111f9fb03d359edbfe541e22a358e808e7f99;
        leaves[1] = 0x2df8ac6ecf7f28b44957f25f60af142127d676df7ffc1ee07b482e3e61e6d0e0;
        leaves[2] = 0x8774f1b9238777b6cc84b19b61375c25d17c5ffb81b49d7867e53dd53a2d1c29;
        leaves[3] = 0x6dde740fd107c3ec125cfce0e12e020d6a656b9f636654f516867922e1b73cfc;
        leaves[4] = 0x15cab1f153f0b469f9f11c4f75c7e6358983a0f9ba8b390ae0e439454988a737;
        leaves[5] = 0xb9bb3c3782e718eb62b311934f51c595a62555a2344a5564c6e2291ddb766c69;
        leaves[6] = 0xd59c5ba34df0459fb8b6626162244c31d2bade592cd95d2824f21db99cc37389;
        leaves[7] = 0x20f4a65c98e92f0a10f3ea595ad3e34e9ab3c57656c8cf098058fc22738e6170;
        leaves[8] = 0xd3cc1a31770b2e9786e3d22f78a94ca7b637bb3ce50e95fe0bdb8534f91d9608;
        leaves[9] = 0x819d26a1d98df6c99b3891e6357c2f7676d382969ab8046fa4da77d9e78b9db5;
        leaves[10] = 0x084a2d215f2d2b0186b9623e2d8699d828f573fc66e7b2f00999d5ae04376796;
        leaves[11] = 0xe7493dbd5383b19f9347b7fa66e35d0cf27e3c343e7ae23c63d170081446d16c;
        leaves[12] = 0x58d1f6881cb108879b0bea94a360e350f88a7e4e7672f1c70b1d39ead54c389b;
        leaves[13] = 0xcb7b65b161895502f02ea100cd1e89e3a7629225b5b96a4ef9f29a12043bf04a;
        leaves[14] = 0x1a47ddd44cce3cbfa92579281419e1d1615ec1a852cd071df51f68ce5e5d9289;
        leaves[15] = 0xbab9aca18c70e6f261180f71eea62ac9280dd9acbfc5555d3d147277f3a5c6d1;
        leaves[16] = 0xcdb33f1a13b148c0275b31c768834ed686f68843297b9364fc5415e8d8680e42;
        width = 32;
        root = 0x93bbee5dd4dc0070239291d3aa54ad6ad53961a2f509c4bdf33ca4cebe29a121;
        indices = new uint256[](17);
        siblings = new bytes32[][](17);
        indices[0] = 0;
        siblings[0] = new bytes32[](5);
        siblings[0][0] = 0xdba39d64837f7d0b94494e8494dd76cc2d5532b3bfeb2bf4ad24c783409fe7d0;
        siblings[0][1] = 0x44e39f72b6b08511791f14de407ecf2733383755d10fcb1555602a7ca8d7afb5;
        siblings[0][2] = 0xc42c45bed9133b35572da43f4d5b2f147663a29affff82981e9a0ba52cc7998e;
        siblings[0][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[0][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[1] = 1;
        siblings[1] = new bytes32[](5);
        siblings[1][0] = 0xfc8c75b8de8d264aa6333106cf488b3769ac82849935bd7bafc1a0fcab4892d3;
        siblings[1][1] = 0x44e39f72b6b08511791f14de407ecf2733383755d10fcb1555602a7ca8d7afb5;
        siblings[1][2] = 0xc42c45bed9133b35572da43f4d5b2f147663a29affff82981e9a0ba52cc7998e;
        siblings[1][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[1][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[2] = 2;
        siblings[2] = new bytes32[](5);
        siblings[2][0] = 0xd52ca63a56b081173952c2d58c7b3d726c709a490ffdfa8c7dd067b62d41cec1;
        siblings[2][1] = 0xe7ae667d2d7f97bd3a8afb11c0d64c3db3dcea8e8249a9463b9c2d207f9d4f4f;
        siblings[2][2] = 0xc42c45bed9133b35572da43f4d5b2f147663a29affff82981e9a0ba52cc7998e;
        siblings[2][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[2][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[3] = 3;
        siblings[3] = new bytes32[](5);
        siblings[3][0] = 0x029831c5be861951b44a636933ab0d560c75113846f01ce989e4a80e9d6a325a;
        siblings[3][1] = 0xe7ae667d2d7f97bd3a8afb11c0d64c3db3dcea8e8249a9463b9c2d207f9d4f4f;
        siblings[3][2] = 0xc42c45bed9133b35572da43f4d5b2f147663a29affff82981e9a0ba52cc7998e;
        siblings[3][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[3][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[4] = 4;
        siblings[4] = new bytes32[](5);
        siblings[4][0] = 0x5e055903898fde0b8d845f692d0e62c6f0788384ee1b38201669b7f6e547887e;
        siblings[4][1] = 0x52e396c87376c92fff9e9868a871a04005f7f83fd09ff1ab933a87034019330e;
        siblings[4][2] = 0xab84b4e42f49b5f076a214a03f6dd9d36b5a0b17c6d19aedac99fdf4e7ce4b34;
        siblings[4][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[4][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[5] = 5;
        siblings[5] = new bytes32[](5);
        siblings[5][0] = 0x29c22b6de6120d0f2a4fb7581998bc42f4adc57051ae8465d07294f7ededaef7;
        siblings[5][1] = 0x52e396c87376c92fff9e9868a871a04005f7f83fd09ff1ab933a87034019330e;
        siblings[5][2] = 0xab84b4e42f49b5f076a214a03f6dd9d36b5a0b17c6d19aedac99fdf4e7ce4b34;
        siblings[5][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[5][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[6] = 6;
        siblings[6] = new bytes32[](5);
        siblings[6][0] = 0x18b325a1b044be2b301f73eb07450df4e86503c2eab5947a77084821eabbf076;
        siblings[6][1] = 0x08a61ee2122319c9feb241b951df73cd79dccead043d7ad1c472a50e122eda67;
        siblings[6][2] = 0xab84b4e42f49b5f076a214a03f6dd9d36b5a0b17c6d19aedac99fdf4e7ce4b34;
        siblings[6][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[6][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[7] = 7;
        siblings[7] = new bytes32[](5);
        siblings[7][0] = 0xac835596ff37cc5259ee02db1ccc84168ffded7efad5fa62634593d0cb55e001;
        siblings[7][1] = 0x08a61ee2122319c9feb241b951df73cd79dccead043d7ad1c472a50e122eda67;
        siblings[7][2] = 0xab84b4e42f49b5f076a214a03f6dd9d36b5a0b17c6d19aedac99fdf4e7ce4b34;
        siblings[7][3] = 0x7b23da17dd5d5f49d612900689eb9f121822f3f1ccbe0d4fbad7d377aef05ab4;
        siblings[7][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[8] = 8;
        siblings[8] = new bytes32[](5);
        siblings[8][0] = 0x3785640aee52d351b20cdf470cbaeaac8b541786c6adb2d5e0812c4f80caac9f;
        siblings[8][1] = 0x6b94ba38c10013f5faec5a6a7b9293815c9c61b4867bb647918e97455411f2dc;
        siblings[8][2] = 0xa281ac10d81163bfafc7ee12083e24041f4396f9d9a3cc75bc7e96d3506652cd;
        siblings[8][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[8][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[9] = 9;
        siblings[9] = new bytes32[](5);
        siblings[9][0] = 0xc730a7492bf5f472024ecd84ef320efa04156e17ccfadcb44b643f1b8c09aef3;
        siblings[9][1] = 0x6b94ba38c10013f5faec5a6a7b9293815c9c61b4867bb647918e97455411f2dc;
        siblings[9][2] = 0xa281ac10d81163bfafc7ee12083e24041f4396f9d9a3cc75bc7e96d3506652cd;
        siblings[9][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[9][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[10] = 10;
        siblings[10] = new bytes32[](5);
        siblings[10][0] = 0x93fb629a8e3cec24ff6af754a0e731ad389d01830f2ae549686a6293639b3aa9;
        siblings[10][1] = 0xf69270cc23ce6787f893d54aa0182bcb5eb66d15b5c2f49a6451390101e2c82b;
        siblings[10][2] = 0xa281ac10d81163bfafc7ee12083e24041f4396f9d9a3cc75bc7e96d3506652cd;
        siblings[10][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[10][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[11] = 11;
        siblings[11] = new bytes32[](5);
        siblings[11][0] = 0xf8e06a138ff8da89d391f64841e6cebe7b4b0317b9b8e4bbb81a1c92eb0c9a6a;
        siblings[11][1] = 0xf69270cc23ce6787f893d54aa0182bcb5eb66d15b5c2f49a6451390101e2c82b;
        siblings[11][2] = 0xa281ac10d81163bfafc7ee12083e24041f4396f9d9a3cc75bc7e96d3506652cd;
        siblings[11][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[11][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[12] = 12;
        siblings[12] = new bytes32[](5);
        siblings[12][0] = 0x22dc49edc3fbe655bc22d738b7f2fda345cb4154a864a91d8f2f20d37407e1c1;
        siblings[12][1] = 0xbac9962244be4d9555fab3dce80a0afde8534863dbabdbb4f453a3308783d90a;
        siblings[12][2] = 0x519e12fb70b8ffc803023dfc6aa6e6882cd4a10ffbe1cc9523d69c8f781d5c43;
        siblings[12][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[12][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[13] = 13;
        siblings[13] = new bytes32[](5);
        siblings[13][0] = 0xe4994f5f55de6d7b8243c865f8d5aa875ad4036bc54cb77a971d91ac35b8b93f;
        siblings[13][1] = 0xbac9962244be4d9555fab3dce80a0afde8534863dbabdbb4f453a3308783d90a;
        siblings[13][2] = 0x519e12fb70b8ffc803023dfc6aa6e6882cd4a10ffbe1cc9523d69c8f781d5c43;
        siblings[13][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[13][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[14] = 14;
        siblings[14] = new bytes32[](5);
        siblings[14][0] = 0xcba280076d8e483a77ebbbe2aa0f4bbcd4aa740913ffd710c69f8bf116c6fc40;
        siblings[14][1] = 0x8228cec37c94447e169fe7a27595c7931323a79998ff510743cd9395a1a805d7;
        siblings[14][2] = 0x519e12fb70b8ffc803023dfc6aa6e6882cd4a10ffbe1cc9523d69c8f781d5c43;
        siblings[14][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[14][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[15] = 15;
        siblings[15] = new bytes32[](5);
        siblings[15][0] = 0xb03ffc11e495a426d565b848588d2cb21672484ad8596705c5ec32e7f68224f6;
        siblings[15][1] = 0x8228cec37c94447e169fe7a27595c7931323a79998ff510743cd9395a1a805d7;
        siblings[15][2] = 0x519e12fb70b8ffc803023dfc6aa6e6882cd4a10ffbe1cc9523d69c8f781d5c43;
        siblings[15][3] = 0xd22c8bbcd55f214c2bc6c3d74b4449e4f1a5928aa7a72387e3ca022249d74013;
        siblings[15][4] = 0x21f81b3f31ba891a68943f4e58d721278e5346e1eab710ecb06712495e048f2c;
        indices[16] = 16;
        siblings[16] = new bytes32[](5);
        siblings[16][0] = 0xf39a869f62e75cf5f0bf914688a6b289caf2049435d8e68c5c5e6d05e44913f3;
        siblings[16][1] = 0x4ed5c02d6d48c8932486c99d3ad999e5d8949dc3be3b3058cc2979690c3e3a62;
        siblings[16][2] = 0x1c792b14bf66f82af36f00f5fba7014fa0c1e2ff3c7c273bfe523c1acf67dc3f;
        siblings[16][3] = 0x5fa080a686a5a0d05c3d4822fd54d632dc9cc04b1616046eba2ce499eb9af79f;
        siblings[16][4] = 0x0bd2ef9a6891c0b82444a177d750f1145c3571191f47e53d31f4976699f6f689;
    }

    uint256 internal constant TREE_COUNT = 10;
    bytes32 internal constant EMPTY_ROOT = 0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470;

    /// @notice Returns one tree's vectors by index.
    function tree(uint256 i) internal pure returns (
        bytes32[] memory leaves,
        uint256 width,
        bytes32 root,
        uint256[] memory indices,
        bytes32[][] memory siblings
    ) {
        if (i == 0) return _tree0();
        if (i == 1) return _tree1();
        if (i == 2) return _tree2();
        if (i == 3) return _tree3();
        if (i == 4) return _tree4();
        if (i == 5) return _tree5();
        if (i == 6) return _tree6();
        if (i == 7) return _tree7();
        if (i == 8) return _tree8();
        if (i == 9) return _tree9();
        revert("MerkleVectors: tree index out of range");
    }
}
