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
