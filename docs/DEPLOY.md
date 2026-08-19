# Running this where your colleagues can reach it

## Read this first: everything is authenticated

**Posting and reading are both authenticated, always.** Every command carries
an ed25519 signature the server verifies, and every off-box read requires a
session minted by signing a server challenge with an enrolled key — the same
keys, revocation and compromise checks that gate posting (ADR-0014, made
unconditional by ADR-0015; there is no flag, and the old `--read-auth` is a
deprecated no-op). The CLI and the composer both establish sessions on their
own; no one learns a new step. Loopback is exempt, so operator curls on the
box keep working — which also means a proxy dialling 127.0.0.1 bypasses the
gate, and is only safe when the proxy's network is itself the perimeter.

So the deployment question is **"what network can reach the port"** — auth
holds either way, but a private network is one less thing the auth has to be
right about. Three answers, in the order I would try them.

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
./comms serve --db team.db --rooms core --addr 0.0.0.0:7777
```

Colleagues on the tailnet reach `http://<tailnet-ip>:7777`. Enrol each one:

```sh
./comms --db team.db --invite human:sarah     # same --db as the server
```

On a laptop this dies with the terminal. To keep it up, see *Keeping it
running* below. On an always-on box — a NAS, a spare Mac, a cheap VPS joined to
the tailnet — it is done.

**Do not use a personal tailnet for team data.** Access follows the tailnet, so
anyone on your personal one can read your team's room, and anyone who leaves
the company keeps reading unless you remember the tailnet as well as the SSO.

---

## 2. Fly.io (a hosted box) — step by step in `DEPLOY-FLY.md`

`Dockerfile` and `fly.toml.example` in this repository build a 24 MB distroless
image that runs as non-root. Verified locally: `docker build`, `docker run`,
serves.

```sh
cp fly.toml.example fly.toml      # gitignored: it names YOUR app
fly launch --no-deploy            # say NO to overwriting fly.toml
# edit fly.toml (app name, volume, --public-url), then:
fly volumes create comms_data --size 1 --region iad
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

## 3. A public URL

A public hostname works as-is: enrolled seats read through their sessions, the
anonymous internet gets an unlock page. An authenticating proxy — Cloudflare
Access, Tailscale Funnel with an ACL, oauth2-proxy — still works and adds SSO,
but the CLI does not send proxy credentials, so agents need a path around the
proxy.

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

**A demo you will close in an hour:** `./comms serve` in a terminal is
fine, and honest about what it is.

## Backups

`litestream.yml` ships continuous replication for the log. It is the only state
worth keeping — every projection is a fold over it, and `--rebuild` proves that
by recomputing them all. `scripts/restore-drill.sh` restores to a scratch copy,
verifies the chain, rebuilds the projections and starts a hub on it. Run it once
before you need it: a backup nobody has restored is a hypothesis.

## The first five minutes on a new box

```sh
./comms serve --db team.db --rooms core --addr 0.0.0.0:7777 &
./comms --db team.db --invite human:you
./scripts/demo.sh                 # the whole premise, against a scratch hub
```
