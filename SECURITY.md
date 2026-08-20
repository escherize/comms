# Security

## Reporting a vulnerability

Email bryan.maass@gmail.com, or use GitHub's private vulnerability reporting
on this repository. Please do not open a public issue for anything
exploitable. You'll get an acknowledgement within a few days; fixes ship on
`main` and deploy continuously.

## Model, in brief

- **Writes are signed.** Every entry is Ed25519-signed by an enrolled seat's
  key; the server verifies against the enrolment it holds. There is no
  password anywhere.
- **Reads are authenticated.** Off-box reads require a session minted by
  signing a single-use challenge with an enrolled key (ADR-0014/0015). Reads
  are filtered to the seat's room membership.
- **Loopback is operator trust, seatless only.** Being on the hub's box is
  equivalent to holding the SQLite file, so a *seatless* loopback request
  gets the operator view — but a request that names a seat is bound by that
  seat's scope even from loopback: identity wins over locality.
- **Invites are single-use bearer tokens** that travel as `#setup=` URL
  fragments so they never reach server logs. Treat a token like the seat it
  mints.
- **The log is append-only.** Redaction suppresses content (search index and
  attachments included, atomically with the redact event) but the record of
  the act remains.
- **The browser key is non-extractable** (WebCrypto, IndexedDB); the CLI key
  is a 0600 file outside any git worktree. Neither is ever sent anywhere.

Details: `docs/adr/`, especially 0014 (read sessions) and 0015 (room-scoped
reads, always-on auth).

## Out of scope

- Denial of service against a hub you operate yourself.
- Anything requiring shell access to the hub's box (that is the operator
  trust boundary by design).
- Social engineering of invite tokens handed over out of band.
