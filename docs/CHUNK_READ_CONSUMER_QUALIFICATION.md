# Chunk read errors: pinned Node consumer qualification

The `node-chunk-read-recovery` CI job tests this Core candidate against Node
`86a254841276ede899b22379a9cdea4f9632465a`. Node's actual `zvm-core` gitlink
remains `29e6d9b30be94de3772c850c7a1e9811585a8833`. The job makes an explicit
test-only module override; this is not adoption by Node or an upstream merge.

The same new test first runs against the original Core. Its negative control
must fail at the declared assertion because a corrupt stored chunk is treated
as absent and the actual engine advances to block 7. A compiler failure,
timeout, skip, different assertion or missing native failure is refused.
Each source variant is compiled once. The candidate binary is reused for all
four original default-client recovery commands and the added test.

The positive test uses the unchanged real op-geth defaults, authenticated
catalyst API, file SQLite store, production CLI, original EOA order signatures
and original complete canonical-state oracle. NoM observations are synthetic.
After valid startup at momentum 7 / engine block 6 it separately injects an
invalid chunk author, an unreadable momentum, and a missing chunks table.
At least two actual production source requests for momentum 8, the production
read-error log, the unchanged SQL head and equal unchanged RPC/direct engine
headers must be observed before an owned exit 86 skips service cleanup.

The failed target is retained without repair. Another independent restore of
the untouched valid cold pair must process the same original momentum 8 and
reach momentum 9. All original canonical tables, balances, ownership, orders,
chunks, deferred data and receipt/state roots must match an independent
sequential reference. Initial signatures are never replaced or resubmitted.
The original source and backup must remain byte-identical.

The consumer helper gets exactly one test-only observer call. Every original
test and oracle body is preserved. The module overrides live outside tracked
source; production `go.mod`, `go.sum`, `.gitmodules` and gitlink stay unchanged.
Final source verification checks the exact declared delta and module sums.

The workflow is a qualification candidate until its exact revision and complete
native output are verified. It does not qualify a real operator backup, signed
NoM authority, kernel kill, hardware power loss, other platforms, complete C02
or N04 acceptance, release, deployment or production activation.
