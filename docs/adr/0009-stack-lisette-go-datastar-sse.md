# Stack: Go semantics (via Lisette when practical), datastar over SSE, no SPA

The server targets Go: single static binary, first-class datastar SDK, mature SQLite drivers. Lisette (Rust-syntax language compiling to idiomatic Go) is the preferred authoring language for the functional core once its toolchain proves stable; because it emits idiomatic Go, starting in or ejecting to plain Go preserves identical semantics in both directions. The UI is datastar (~11 KB) driven by SSE element patches — server-side rendering, no SPA framework, no build step.

## Considered Options

A Python/Starlette stack was the first draft and was dropped when the language choice shifted: datastar's reference SDK being Go removed the main reason to stay. An SPA was rejected because a chatroom is SSE's home turf and every added moving part is a maintenance seam a five-person team pays for.
