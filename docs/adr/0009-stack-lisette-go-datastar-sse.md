# Stack: Go, datastar over SSE, no SPA

The server is written in Go: single static binary, first-class datastar SDK, mature SQLite drivers, and a standard library that already covers HTTP, SSE, and templating. Lisette (Rust-syntax compiling to idiomatic Go) remains an option for the functional core later, because it emits idiomatic Go and the semantics are identical in both directions — the door opens inward as well as outward. The UI is datastar (~11 KB) driven by SSE element patches — server-side rendering, no SPA framework, no build step.

## Consequences

Go was chosen for M1 over authoring in Lisette for three reasons that only became concrete during implementation. The security-critical dependencies — goldmark and bluemonday for the artifact display boundary (ADR-0011) — are Go libraries whose correctness we depend on, and a young compiler between us and them adds an interop surface at exactly the place we least want surprises. The Go toolchain (test, vet, coverage, gofmt) is what made a test-first build fast; adding an unproven compiler adds a debugging axis with no compensating gain at this size. And the decider is ~250 lines whose invariants are already enforced by an exhaustive table test.

The one thing Lisette would genuinely buy is compile-time exhaustive matching over event kinds. Go cannot catch a new `Kind` added without a corresponding arm in `knownKind`, `LaneOf`, or `checkBody` — the default arm silently absorbs it. `core/exhaustive_test.go` is the deliberate substitute: it enumerates every kind and asserts each is known, deliberately laned, and postable. If that file is ever deleted, the argument for Lisette gets materially stronger.

## Considered Options

A Python/Starlette stack was the first draft and was dropped when the language choice shifted: datastar's reference SDK being Go removed the main reason to stay. An SPA was rejected because a chatroom is SSE's home turf and every added moving part is a maintenance seam a five-person team pays for.
