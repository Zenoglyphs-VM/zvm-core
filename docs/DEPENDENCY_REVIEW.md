# Dependency and distribution review

The CI action implementations are pinned to full commit IDs. The adjacent
version comments describe their existing major versions. Weekly Dependabot
configuration proposes updates for Actions, Go modules and the shared web
package, with at most two open ordinary version-update pull requests per
ecosystem. Review and qualification are required before accepting an update.

This configuration does not establish that Dependabot has run, that an advisory
scan is clean, that a dependency is unreachable, or that a distribution has
completed license review.

## Review an update

For an action, resolve the intended release in its official repository and
inspect the old-to-new commit diff, action metadata, bundled implementation and
dependency changes. Review requested permissions, credentials, subprocesses,
network destinations and artifact handling. Keep the full commit pin and its
readable version comment together. A matching tag alone is not an independent
review of the implementation.

For Go, review `go.mod` and `go.sum` together. Include the
`github.com/ethereum/go-ethereum` replacement by
`github.com/ethereum-optimism/op-geth` in the effective dependency inventory;
checking only the replaced module's nominal version misses the code that is
actually built. Record the selected Go toolchain and platform separately.
Run build, vet, relevant behavior tests and module-integrity verification in a
bounded qualification environment. Lock or checksum agreement establishes
input consistency, not absence of vulnerabilities.

For `web/package.json`, review peer dependency compatibility in the consuming
application and its committed lock. This shared package has peer ranges and no
standalone application lock at this source revision. Resolve and retain the
consumer's actual versions, integrity records, install-script decisions and
build/test results rather than reporting a range as an installed version.

Record advisory triage against the exact resolved dependency or replacement,
advisory revision and affected versions. State whether the path is used at
runtime, during builds/tests, or only in a non-shipped tool. Link the relevant
call site or build step and a bounded reproducer/check when available. Preserve
unknown reachability explicitly; a scanner match or a missing scanner match is
not a reachability decision. Keep sensitive findings in the repository's private
reporting channel until coordinated disclosure.

## Review the distribution inventory

Start with [`NOTICE.md`](../NOTICE.md) and [`LICENSE`](../LICENSE). The project's
own license does not describe every third-party bytecode or dependency input.
For each shipped binary, web bundle and predeploy bytecode, retain the exact
source revision, input and output digests, build/compiler settings, applicable
upstream license/notice files and the distribution's corresponding material.
Include copied, linked and transitive material that actually reaches the output.
Have the appropriate distribution and license reviewers assess that inventory;
the existing notice table is not a completed review record.

An SBOM and provenance statement must describe a particular built output and
its resolved inputs. A manifest-only dependency list cannot substitute for
that output-bound record. Release signing, complete notices, immutable runner
and image provenance, supported-platform builds and independent verification
remain separate release work.

## Preserve the evidence boundary

The current `ubuntu-latest` runner label is mutable. Pinning actions does not pin
the runner image, downloaded Go toolchain, module proxy responses or a complete
build environment. Record their actual identities in qualification evidence.
Successful CI qualifies only its exact source, inputs, platform and executed
checks; it does not qualify deployment, live-chain behavior or release approval.

Keep public review records in English and omit private paths, personal identity,
credentials, wallet material and raw private diagnostics. Preserve source pins,
actual exit/completeness results and explicit skipped or unknown checks without
copying those private details into a pull request.

References: [GitHub Actions secure use](https://docs.github.com/en/actions/reference/security/secure-use)
and [Dependabot options](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference).

## Bounded source vulnerability review

The `Dependency review` workflow runs the pinned govulncheck v1.9.0 source
scanner against the exact selected commit, including test packages. The scanner
is built with Go 1.27.4 in a separate tool module/cache, then analysis uses
Go 1.25.14. The scanned source is this workflow's exact checkout; its findings
are not an analysis of historical retained binaries. The selected SDK does not
choose an accepted release toolchain or qualify other platforms. The original
build/vet/test workflow remains unchanged.

The wrapper checks the complete regular-file Git tree, tracked modes and module
locks before and after analysis. Gitlinks, links, untracked/ignored files and
changed inputs require a separately supported profile. A ten-minute process
deadline, process-group termination on budget refusal, an 8 MiB stdout cap and a
256 KiB stderr cap bound collection. GOMAXPROCS and GOMEMLIMIT are workload
controls; GOMEMLIMIT is not an operating-system hard memory limit. The hosted
job has a separate fifteen-minute deadline. Local protocol tests do not claim
the hosted process controls or actual scanner execution.

The producer's actual exit, stream completeness, hashes, scanner digest and
source pins are retained in `execution.json`. `govulncheck.json` and stderr are
kept even on failure. A valid JSON prefix, an absent report or exit zero alone
cannot establish success: govulncheck's JSON mode exits zero even when it emits
findings. A recognized complete protocol, expected scanner/database/Go/mode,
scan inventory and matching advisory references are required. Incomplete,
malformed, mismatched, timed-out or capped scans exit 2. Any finding, including
module-only and package-only reports, exits 1 and requires review. A complete
scan without findings exits 0 with `NO_FINDINGS_REPORTED_IN_THIS_SCAN`.

`review.json` separates module, package and source-call reports, retains missing
fix versions as unknown and counts repeated findings separately from distinct
advisories. The parser also labels binary symbols separately from source call
traces; this workflow executes source mode only. Source locations and advisory
prose are omitted from the summary. The raw report remains necessary for call
site and runtime/build/test classification. Review public artifacts for privacy
before using these tools on any private source.

A source call reported by static analysis is not exercised exploitability.
Reflection, unsafe code, excluded build configurations and unknown module
versions require separate review. An unreported advisory does not establish an
unaffected dependency. In particular, the effective op-geth replacement needs
explicit upstream/fork advisory mapping; checking only one namespace is not
complete fork coverage. Database modification time and retained OSV digests
identify observations, not an authenticated or complete database snapshot.
Actual updater operation, remediation, distribution notices and all D04 release
and owner acceptance requirements remain open. No waivers are generated.

Reports are uploaded for the exact automatic workflow attempt, including failed
attempts. They are review evidence, not accepted project provenance, a release
SBOM, a deployment authorization or a clean-security claim. No manual CI
trigger, retry, signing or release path is added.

Protocol and limits: [govulncheck v1.9.0](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck@v1.9.0),
[stream protocol](https://github.com/golang/vuln/blob/e672cbe8fbc8c76e28fcb6db0efd9ce354e0dac5/internal/govulncheck/govulncheck.go),
and [Go vulnerability database](https://go.dev/doc/security/vuln/database).

## Repeated effective module identities

Test-package scans and replacement resolution can report the same effective
module path/version more than once. The consumer records the raw occurrence
count and counts distinct path/version pairs separately. Different versions
remain separate records. This normalization applies only to the scan inventory;
it does not remove findings, collapse their levels or merge advisory revisions.
An initial complete producer stream that exercised this case remains retained
under its original source/attempt; consumer replay is separate from a new scan.
