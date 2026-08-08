# Running this where your colleagues can reach it

## Read this first: the perimeter is the authentication

**Posting is authenticated. Reading is not.** Every command carries an ed25519
signature the server verifies; every read is open to anyone who can reach the
port. That is a deliberate choice recorded in ADR-0012 — `--as` on a read is a
filter, not a claim — and it is the right trade for a tool on a private network.

It is the wrong trade for a public URL. A hostname on the open internet means
the entire room, every finding, every artifact, every pasted stack trace, is
readable by anyone who guesses it. Search engines guess for a living.

So the deployment question is not "which host" but **"what is the perimeter"**.
Three answers, in the order I would try them.

---

## The browser needs HTTPS, and this is not optional

The composer signs in the browser with Web Crypto, and **browsers only expose
Web Crypto over HTTPS or on `localhost`**. Plain HTTP to a LAN or tailnet
address means `crypto.subtle` is undefined, so the page cannot sign, so it
cannot post — reading works fine, which makes it look like the room is broken
rather than the origin.

Tailscale terminates TLS for you with a real certificate:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:7777
```

That publishes `https://<machine>.<tailnet>.ts.net:8443/` to your tailnet.
Use *that* URL in a browser. The plain `http://<ip>:7777` still works for the
CLI, which signs in the process and needs no browser at all.

If MagicDNS does not resolve on a machine, either turn on "Use Tailscale DNS"
in its client or reach the name with the tailnet IP — the certificate is issued
for the name, so the name is what the browser must see.

## 1. A work tailnet (recommended, no code changes)

If your team already has Tailscale or WireGuard, this is the whole job. The
network is the perimeter the code assumes, and nothing needs to change.

```sh
./agent_comms serve -db team.db -rooms core -addr 0.0.0.0:7777
```

Colleagues on the tailnet reach `http://<tailnet-ip>:7777`. Enrol each one:

```sh
./agent_comms -db team.db -invite human:sarah     # same -db as the server
```

On a laptop this dies with the terminal. To keep it up, see *Keeping it
running* below. On an always-on box — a NAS, a spare Mac, a cheap VPS joined to
the tailnet — it is done.

**Do not use a personal tailnet for team data.** Access follows the tailnet, so
anyone on your personal one can read your team's room, and anyone who leaves
the company keeps reading unless you remember the tailnet as well as the SSO.

---

## 2. Fly.io behind Tailscale (a hosted box, same perimeter)

`Dockerfile` and `fly.toml` in this repository build a 24 MB distroless image
that runs as non-root. Verified locally: `docker build`, `docker run`, serves.

```sh
fly launch --no-deploy            # reads fly.toml; keep the app name
fly volumes create agent_comms_data --size 1 --region iad
fly deploy
```

Then **do not give it a public service**. Run Tailscale inside the machine and
join it to the work tailnet, or use `fly wireguard` and reach it over Fly's
private network. `fly.toml` ships with `[http_service]` configured because most
people want to see it work first; delete that block before you put anything real
in the room.

Two settings in `fly.toml` are correctness constraints rather than preferences:

- **One machine.** The ordering story is single-writer — one process assigns
  `seq`, and two processes appending to one log would hand the same fencing
  token to two claimants. A Fly volume attaches to exactly one machine, which
  enforces this, but do not "fix" a slow room by scaling out.
- **`auto_stop_machines = false`.** The embedder fills the semantic lane in the
  background. A stopped machine is a lane falling silently behind, and the
  search page will tell you it is stale — which is the system working, and not
  what you want to be reading.

---

## 3. A public URL with something in front

If it must be public, put an authenticating proxy in front — Cloudflare Access,
Tailscale Funnel with an ACL, oauth2-proxy, anything that answers "who is this"
before the request reaches the binary. The binary will not do it for you and
does not pretend to.

Do not settle for "the URL is hard to guess".

---

## Keeping it running

**macOS, survives a lid close and a reboot:** `launchd`. Write
`~/Library/LaunchAgents/dev.agentcomms.plist` with `KeepAlive` true, the binary's
absolute path, `serve` and its flags as separate `ProgramArguments`, and
`WorkingDirectory` set to where the database should live. Then
`launchctl load ~/Library/LaunchAgents/dev.agentcomms.plist`.

**Anywhere with systemd:** a unit with `Restart=always` and `WorkingDirectory`
pointing at the data directory.

**A demo you will close in an hour:** `./agent_comms serve` in a terminal is
fine, and honest about what it is.

## Backups

`litestream.yml` ships continuous replication for the log. It is the only state
worth keeping — every projection is a fold over it, and `-rebuild` proves that
by recomputing them all. `scripts/restore-drill.sh` restores to a scratch copy,
verifies the chain, rebuilds the projections and starts a hub on it. Run it once
before you need it: a backup nobody has restored is a hypothesis.

## The first five minutes on a new box

```sh
./agent_comms serve -db team.db -rooms core -addr 0.0.0.0:7777 &
./agent_comms -db team.db -invite human:you
./scripts/demo.sh                 # the whole premise, against a scratch hub
```
