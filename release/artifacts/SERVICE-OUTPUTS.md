# Current service source and output qualification

The [source cohort snapshot](qualification/service-source-darwin-arm64.json)
records independent pre-build expectations for the current Node, Relayer and
Bridge sources. It accompanies the [local-source profile](LOCAL-SOURCES.md)
and [contents schema 2](CONTENTS.md). It is qualification data, not a release
manifest or a policy automatically accepted by either command.

## Source cohort

All three repositories select Core commit
`29e6d9b30be94de3772c850c7a1e9811585a8833`, tree
`86f8c8cb55fb298674a73242a928141eebc1dee6`.
The complete Core snapshot has 112 regular files and 1,155,787 source bytes.
Every parent regular file, executable mode and Core Gitlink was included when
independently reproducing each parent Git tree and commit binding.

| Service | Selected parent commit | Parent files | Parent bytes |
| --- | --- | ---: | ---: |
| Bridge | `3f8f1197a91e530590f9e1c21ae55d358f62296d` | 33 | 214,717 |
| Relayer | `5a72a223dd60d1ae4e974d420cadd85e6f9ee929` | 23 | 170,231 |
| Node | `ae90b945b0ea95887c31cfb2f19e0b88504186d8` | 110 | 2,062,637 |

Sources were read once from retained public Git objects. No new repository,
checkout or dependency download was needed. Explicit file/member references
allow the same inputs to be reused without duplicating entire dependency trees.
Raw Git author/message data and physical runtime directories are excluded from
the public snapshot.

The [standalone source tree preflight](SOURCE-TREES.md) can check a complete parent
export and its selected Gitlink entries against a separately pinned source policy.
Check each child tree separately when child content is needed. A source-only
receipt does not establish compiler use or upgrade an artifact/SBOM receipt.

## Actual package observations

The recorded source graphs used Go 1.25.14 with `GOOS=darwin`, `GOARCH=arm64`,
`GOARM64=v8.0` and `CGO_ENABLED=0`, with network access denied. Exact parent and
Core exports contained no Git metadata. The Go commands used read-only module
inputs and a read-only compiler cache. All source exports, cache observations
and module inputs matched after the commands; parent cache growth was zero.

| Service | Packages | Remote modules | Local modules | Selected parent files | Selected Core files | Selected standard-library files |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Bridge | 664 | 89 | 1 | 15 | 36 | 1,250 |
| Relayer | 620 | 80 | 1 | 6 | 36 | 1,250 |
| Node | 674 | 88 | 1 | 61 | 38 | 1,264 |

Each native `go list -e -deps` observation completed with exit 0 and zero
incomplete packages, package errors or dependency errors. `-e` alone is not a
success criterion: every error-bearing package must be inspected. Counts cover
the selected command package and its dependencies, without test packages.

The snapshot includes every expected remote module coordinate, original and
effective replacement identity, and source-lock Go h1 sum. The single local
Core record keeps its requested version and selected commit/tree; no h1 sum is
invented for local source. These expectations came from source/package inputs,
before any new executable was built, rather than from binary build information.

The 1,264 distinct selected standard-library source files were content-hashed
against the retained SDK distribution manifest. The snapshot reports each
cohort's selected-file count and manifest digest. These digests cover a JSON
object mapping SDK-relative filenames to SHA-256, serialized as UTF-8 with keys
sorted, no whitespace and no trailing newline. They are source-input evidence;
the whole current SDK, compiler use and complete runtime inventory are not
established by that selected-file check.

To repeat the source observation with independently qualified inputs, select the
exact commits and Core Gitlink, use the recorded Go tool/version and target, and
run the relevant command package:

```sh
go list -p=1 -mod=readonly -trimpath -buildvcs=false -e -deps \
  -json=Dir,ImportPath,Name,Module,Standard,GoFiles,CgoFiles,CFiles,CXXFiles,HFiles,SFiles,SysoFiles,EmbedFiles,EmbedPatterns,Imports,DefaultGODEBUG,Incomplete,Error,DepsErrors \
  ./cmd/zvm-relayer
```

Use the corresponding `./cmd/zvm-node` or `./cmd/zvm-bridge` package for the
other cohorts. Keep Go environment/toolchain configuration fixed, disable
network/module resolution and inspect errors. Raw graph output contains runtime
directories and requires privacy review before sharing.

## Docker and release boundaries

The selected Dockerfiles use `golang:1.25-alpine` and `alpine:3.22` tags,
`CGO_ENABLED=0`, `-trimpath` and linker flags `-s -w`. The command package and
exact Dockerfile, ignore-file, module, checksum, workflow and Gitlink-file pins
are in the snapshot. The image tags and runtime package resolution remain
mutable. A Darwin source graph does not qualify a Linux image or resolve its
target architecture, toolchain, OS packages, runtime assets or licenses.

The captured five-repository inventory had no tags or releases. The `.github`
repository had no default branch. This records the observation, not release
policy or maintainer approval. All five original D02 adopters remain in scope:
`.github`, Core, Node, Relayer and Bridge.

The next output cohort needs a retained actual service executable from the
independently selected source/recipe/platform, followed by subject-bound SPDX
generation/check and complete contents verification. Reuse these source-side
module expectations; do not reconstruct expectations from the resulting binary.
Record any extra qualification flags and their difference from the Docker recipe.
Keep prior qualification fixtures and older project builds bound to their own
sources.

Accepted image/toolchain/build recipes, real project attestations and trusted
roots, immutable tags/checksums/notes/SBOM/package sets, supported-platform
outputs, independent distribution/reproduction and runtime/license coverage
remain open. Source and package closure does not run the services, signers,
wallets or a chain. D01/V02/epoch and maintainer/owner/operator release gates
remain separate. `releaseAccepted` stays false.

## Retained Bridge package observation

The [Bridge package record](qualification/bridge-package-darwin-arm64.json) adds
one actual Go 1.25.14 darwin/arm64 build at the selected Bridge/Core commits.
Source-bound SPDX verification checks 89 remote modules and one local Core
module. Complete contents verification binds all 33 parent files and 112 Core
files, their raw commit controls and the selected Core Gitlink. The older
local-source contents mode continues to report no parent-source verification.

The archive layer reuses that exact executable, SPDX and contents manifest;
it does not rebuild the service. Four parent/SPDX refusals and an independent
archive-digest refusal complement the positive observations. The initial worker's
read-only-clone preparation failure and cleanup hold are preserved, with its
completed commands reused rather than repeated.

This is a local synthetic release context. The service was not executed, and no
project signature or accepted release was created. The separate receipt fields
keep parent/source observation, SPDX contents and archive-format verification
distinct. Linux images, additional supported platforms, runtime/license coverage,
independent reproduction and maintainer/owner release gates remain open.

## Bridge SPDX schema and semantic checks

The [Bridge package record](qualification/bridge-package-darwin-arm64.json) also
records a new official-schema check of the exact retained 98,068-byte SPDX
document. Pinned SPDX 2.3.1 schema bytes are compiled with Ajv2019 8.17.1 using
the schema's declared 2019-09 dialect. The original document passes, and six
malformed controls are refused. Only 478,592 selected JS/JSON bytes are extracted
from five retained, checksum-pinned validator packages; no install or network
resolution occurs.

A changed artifact checksum still passes the JSON schema. The same changed
document is then refused by the actual Go semantic-profile checker, while the
original document passes against the exact subject, module/lock inputs and 112
Core source files. This separates document shape from subject/source binding.
The existing Node runtime had changed since the earlier schema observation;
this new check pins and records the current runtime rather than reusing the old
runtime result. No source/toolchain authority or accepted release is established.

The service, SPDX and archive are not rebuilt, regenerated or distributed by
this addition. All earlier native, contents, archive and failed-preparation
observations retain their own scope and receipts. Runtime/license coverage,
independent reproduction and project signing/release acceptance remain open.

## Retained Node package observation

The [Node package record](qualification/node-package-darwin-arm64.json) adds one
actual Go 1.25.14 darwin/arm64 executable at the selected public Node/Core commits.
Its source-side graph has 674 packages, 88 remote modules and one local Core
module. Source-bound SPDX checks the retained executable, module/lock inputs and
112 Core files. The explicit contents check also binds all 110 Node files, their
raw commit control and the exact Core Gitlink; repeat output is identical and
legacy mode retains its false parent-source result.

The exact Node SPDX bytes separately pass the pinned official SPDX 2.3.1 schema
with Ajv2019 8.17.1 and six malformed controls. A schema-valid changed subject
checksum is refused by the actual Go semantic checker while the original passes.
The retained schema packages, current pinned Node runtime and Go tools are reused.
Source, schema, semantic and archive checks share one bounded foreground job.

The canonical archive uses that same contents manifest. Two producer streams
match byte for byte; the Go consumer and a separate ZIP reader check seven
entries, and an incorrect archive digest is refused. This stage performs no new
service build. No dependency download, cache-tree copy or service execution is
needed; the executable, SPDX and archive remain undistributed qualification data.

The context is synthetic. Project signing, accepted recipes/toolchains/images,
additional supported platforms, runtime/license coverage, independent
reproduction and owner/operator release acceptance remain open. All five
original D02 adopters retain their original acceptance requirements.
