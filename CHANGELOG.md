## [1.8.1](https://github.com/tawanorg/claude-sync/compare/v1.8.0...v1.8.1) (2026-04-06)


### Bug Fixes

* fail init when storage bucket does not exist ([11ad555](https://github.com/tawanorg/claude-sync/commit/11ad555dc2fd0a50e1224702b8612d9a5cfd583d)), closes [#21](https://github.com/tawanorg/claude-sync/issues/21)

# [1.8.0](https://github.com/tawanorg/claude-sync/compare/v1.7.0...v1.8.0) (2026-04-01)


### Features

* add NewSyncerWith for dependency-injected testing ([60c21bc](https://github.com/tawanorg/claude-sync/commit/60c21bc3fd931b06e24581442ab68250b4614cf3))

# [1.7.0](https://github.com/tawanorg/claude-sync/compare/v1.6.3...v1.7.0) (2026-03-31)


### Bug Fixes

* add commands directory to SyncPaths ([6fb290d](https://github.com/tawanorg/claude-sync/commit/6fb290dbffb96b0797c24368caf626ee7d8780f0)), closes [#14](https://github.com/tawanorg/claude-sync/issues/14)
* remove redundant nil check on map to fix gosimple lint ([f41fbee](https://github.com/tawanorg/claude-sync/commit/f41fbee7d6480171be1b3a26f3f0456d21918fb8))


### Features

* add MCP server sync support ([#15](https://github.com/tawanorg/claude-sync/issues/15)) ([43b1318](https://github.com/tawanorg/claude-sync/commit/43b1318921e0288169e261ecdf25cec251912df7))
* add test coverage check to CI ([a78e191](https://github.com/tawanorg/claude-sync/commit/a78e191d40c5abba6245018a263f10ba4fe37cc8))

## [1.6.3](https://github.com/tawanorg/claude-sync/compare/v1.6.2...v1.6.3) (2026-03-27)


### Bug Fixes

* remove claude-sync auto command ([96c4d73](https://github.com/tawanorg/claude-sync/commit/96c4d73fc9bcb7fc5f17323dd1b2319455bfd2a9))

## [1.6.2](https://github.com/tawanorg/claude-sync/compare/v1.6.1...v1.6.2) (2026-03-27)


### Bug Fixes

* remove auto push/pull hooks to prevent session startup errors ([54d0bfe](https://github.com/tawanorg/claude-sync/commit/54d0bfe82e28cc750e57a498f685bf9f2f3ab8eb))

# [1.6.0](https://github.com/tawanorg/claude-sync/compare/v1.5.1...v1.6.0) (2026-03-25)


### Features

* add changelog command to view release history ([5581559](https://github.com/tawanorg/claude-sync/commit/5581559f88f7a53665dd1f4f17c2de21752fdafb))

## [1.5.1](https://github.com/tawanorg/claude-sync/compare/v1.5.0...v1.5.1) (2026-03-25)


### Bug Fixes

* update sync state after resolving conflicts ([b533622](https://github.com/tawanorg/claude-sync/commit/b5336220f6eb70e2cf0c6d1696962d5e17625f1a))

# [1.5.0](https://github.com/tawanorg/claude-sync/compare/v1.4.0...v1.5.0) (2026-03-25)


### Bug Fixes

* remove --quiet from auto-sync hooks so users see sync progress ([e620333](https://github.com/tawanorg/claude-sync/commit/e6203336a36d082f09efb2cafc56dd1541697b7f))


### Features

* add auto-sync command for Claude Code hooks ([3ff7628](https://github.com/tawanorg/claude-sync/commit/3ff762801df9c84df134363560ff69e3f93bb1b2))

# [1.4.0](https://github.com/tawanorg/claude-sync/compare/v1.3.0...v1.4.0) (2026-03-25)


### Features

* enhance exclude patterns and add comprehensive tests ([d5d4f8b](https://github.com/tawanorg/claude-sync/commit/d5d4f8b7561a47761eb9ce90b3e84b1e6fc8cc4a)), closes [#9](https://github.com/tawanorg/claude-sync/issues/9) [#9](https://github.com/tawanorg/claude-sync/issues/9)

# [1.3.0](https://github.com/tawanorg/claude-sync/compare/v1.2.2...v1.3.0) (2026-03-25)


### Features

* add exclude list to skip paths during sync ([73735f1](https://github.com/tawanorg/claude-sync/commit/73735f171c9b965a2c0d6eb84b2e7a9701a5351b))
* add exclude list to skip paths during sync ([02e5e4e](https://github.com/tawanorg/claude-sync/commit/02e5e4e00753632ac429b76a046d8454712531e6))
* gzip compression before encryption for faster transfers ([d853d36](https://github.com/tawanorg/claude-sync/commit/d853d36bf2a59f10d95651514a2661bfda08ff5e))


### Performance Improvements

* concurrent uploads/downloads with 10 worker pool ([1b32c63](https://github.com/tawanorg/claude-sync/commit/1b32c637d949bb552df82075ab26bd0ee55fd2da))

## [1.2.2](https://github.com/tawanorg/claude-sync/compare/v1.2.1...v1.2.2) (2026-02-09)


### Bug Fixes

* use record format for hooks instead of array ([8433bda](https://github.com/tawanorg/claude-sync/commit/8433bdaca3eeb40b5827a023a3d5e4636c4e9925))

## [1.2.1](https://github.com/tawanorg/claude-sync/compare/v1.2.0...v1.2.1) (2026-02-09)


### Bug Fixes

* use 'source' instead of 'path' in marketplace.json ([e7253aa](https://github.com/tawanorg/claude-sync/commit/e7253aa5d989fe2e25f4b24a959ba5cdceb0309c))

# [1.2.0](https://github.com/tawanorg/claude-sync/compare/v1.1.0...v1.2.0) (2026-02-08)


### Features

* add Claude Code plugin with marketplace support ([0d33f31](https://github.com/tawanorg/claude-sync/commit/0d33f31c71ad68b8610de0d52b87ae25e54fe384))

# [1.1.0](https://github.com/tawanorg/claude-sync/compare/v1.0.0...v1.1.0) (2026-02-08)


### Features

* add batch delete for faster remote clearing ([ce32f4e](https://github.com/tawanorg/claude-sync/commit/ce32f4e37b1e6d009c39af3c5a1b7849f3512a33))

# [1.0.0](https://github.com/tawanorg/claude-sync/compare/v0.6.1...v1.0.0) (2026-02-08)


### Features

* add safety checks for pull with existing files ([87dad45](https://github.com/tawanorg/claude-sync/commit/87dad45894e634a9935458e5b8316864b7a7b23b))


### BREAKING CHANGES

* init --passphrase now only re-enters passphrase (keeps storage config)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>

## [0.6.1](https://github.com/tawanorg/claude-sync/compare/v0.6.0...v0.6.1) (2026-02-08)


### Bug Fixes

* skip npm publish if version already exists ([e5dca87](https://github.com/tawanorg/claude-sync/commit/e5dca8735372fca820bf28d943a769571f15a89d))

# [0.6.0](https://github.com/tawanorg/claude-sync/compare/v0.5.0...v0.6.0) (2026-02-08)


### Features

* add demo gif and logo ([98d2394](https://github.com/tawanorg/claude-sync/commit/98d2394ad4278d884dcdbb27a90253389b8e65ee))

# [0.5.0](https://github.com/tawanorg/claude-sync/compare/v0.4.1...v0.5.0) (2026-02-08)


### Bug Fixes

* bust GitHub cache for banner image ([d5738fd](https://github.com/tawanorg/claude-sync/commit/d5738fda033f027077b78e89ca1648302886cc68))
* fetch latest version from GitHub API during npm install ([fcfd2ce](https://github.com/tawanorg/claude-sync/commit/fcfd2ce6ef324ef1f9c91a55b941fc8b0712267e))


### Features

* publish to GitHub Packages ([e04f65b](https://github.com/tawanorg/claude-sync/commit/e04f65bf342516972885a74b01acdcb99bb5e0aa))

## [0.4.1](https://github.com/tawanorg/claude-sync/compare/v0.4.0...v0.4.1) (2026-02-08)


### Bug Fixes

* use scoped npm package name @tawandotorg/claude-sync ([a0dde3d](https://github.com/tawanorg/claude-sync/commit/a0dde3d8d26a115c794c8b153f1952ad452c3a50))

# [0.4.0](https://github.com/tawanorg/claude-sync/compare/v0.3.2...v0.4.0) (2026-02-08)


### Bug Fixes

* remove unused promptInput function ([0cf28ee](https://github.com/tawanorg/claude-sync/commit/0cf28eeca5a340e85c0ece250fa26a3db75c3cea))


### Features

* add multi-provider storage support (R2, S3, GCS) ([ded6fe8](https://github.com/tawanorg/claude-sync/commit/ded6fe8937dce96c77cba4cac9d904728047b5c1))
* add npm package for easy installation ([2e3c62f](https://github.com/tawanorg/claude-sync/commit/2e3c62f08e32a8b8171a0a27d6e0cc7d9410156a))

## [0.3.2](https://github.com/tawanorg/claude-sync/compare/v0.3.1...v0.3.2) (2026-02-08)


### Bug Fixes

* use git tags for version instead of hardcoded value ([972f0e8](https://github.com/tawanorg/claude-sync/commit/972f0e8da314d47eaff64368c38d80fe5b4a0eca))

## [0.3.1](https://github.com/tawanorg/claude-sync/compare/v0.3.0...v0.3.1) (2026-02-08)


### Bug Fixes

* handle unchecked error returns for linter ([2390505](https://github.com/tawanorg/claude-sync/commit/23905053d96612b43314e4291df644f6bc7763af))

# [0.3.0](https://github.com/tawanorg/claude-sync/compare/v0.2.1...v0.3.0) (2026-02-08)


### Features

* add update command for self-updating CLI ([4c91357](https://github.com/tawanorg/claude-sync/commit/4c9135767dcafdf4b9d78b86e0dd3b84078b22bf))

## [0.2.1](https://github.com/tawanorg/claude-sync/compare/v0.2.0...v0.2.1) (2026-02-08)


### Bug Fixes

* resolve deprecated API warnings ([f1c9559](https://github.com/tawanorg/claude-sync/commit/f1c955937034fe66449bcc87b1542d9c71c58eb7))

# [0.2.0](https://github.com/tawanorg/claude-sync/compare/v0.1.1...v0.2.0) (2026-02-08)


### Features

* add reset command for forgot passphrase recovery ([3cd1cdb](https://github.com/tawanorg/claude-sync/commit/3cd1cdbd1964e108d312e2739fd4938f7a43eaf5))

## [0.1.1](https://github.com/tawanorg/claude-sync/compare/v0.1.0...v0.1.1) (2026-02-08)


### Bug Fixes

* correct Go version to 1.21 in go.mod ([28146ef](https://github.com/tawanorg/claude-sync/commit/28146efb234b699ad96a5e0b4ebcbec80299a21c))
* use Linux-compatible sed in semantic-release ([4a5fb37](https://github.com/tawanorg/claude-sync/commit/4a5fb37697deae23a73db14cfd3c4bca3b34cffc))

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
