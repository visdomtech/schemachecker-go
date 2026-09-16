# Changelog

## 1.0.0 (2026-09-16)


### Features

* add diff subcommand to compare migrations against a baseline SQL file ([#2](https://github.com/visdomtech/schemachecker-go/issues/2)) ([da760f7](https://github.com/visdomtech/schemachecker-go/commit/da760f72b4b821be3789af0fb5b69084c12e0bfe))
* address multi-agent review findings ([535be1e](https://github.com/visdomtech/schemachecker-go/commit/535be1e5cc6152a4b5ddede605a470bbc18d37a8))
* migrate schemachecker from Java/Gradle to Go CLI ([8684298](https://github.com/visdomtech/schemachecker-go/commit/86842987e84b22d27484cbc1a3cc843696ada5cb))
* optimize the schema dump ([#1](https://github.com/visdomtech/schemachecker-go/issues/1)) ([1b368cd](https://github.com/visdomtech/schemachecker-go/commit/1b368cd62190793f8f139b3bc079005006314eb8))


### Bug Fixes

* address remaining review findings ([#18](https://github.com/visdomtech/schemachecker-go/issues/18), [#27](https://github.com/visdomtech/schemachecker-go/issues/27), [#30](https://github.com/visdomtech/schemachecker-go/issues/30), [#31](https://github.com/visdomtech/schemachecker-go/issues/31)) ([27877b4](https://github.com/visdomtech/schemachecker-go/commit/27877b42500cb011bdaf00a31f37d3dd95c24967))
* atlas.sum checksum + search_path restore + strip dump boilerplate ([#3](https://github.com/visdomtech/schemachecker-go/issues/3)) ([0abeefc](https://github.com/visdomtech/schemachecker-go/commit/0abeefc1524a9c5a3754c5243bacf6e2fc2cd9c0))
* defer FK constraints in merge to fix cross-table reference ordering ([#5](https://github.com/visdomtech/schemachecker-go/issues/5)) ([ffcf7b2](https://github.com/visdomtech/schemachecker-go/commit/ffcf7b2dd39f606e2be6f1e96c5d480b674f0fab))
* harden path safety and validate output paths ([5611ac5](https://github.com/visdomtech/schemachecker-go/commit/5611ac5dd888fb5b4640055c14e4430bd2d59bc9))
* preserve IDENTITY columns instead of converting to bigserial ([#6](https://github.com/visdomtech/schemachecker-go/issues/6)) ([55d5169](https://github.com/visdomtech/schemachecker-go/commit/55d51693e096db1b4e2e0cf10c2aeec9c81b8333))
* strip trailing newlines before splitting to match Java .lines() behavior ([edee8eb](https://github.com/visdomtech/schemachecker-go/commit/edee8eb7511dbae824083af51af9ff25e617def5))
