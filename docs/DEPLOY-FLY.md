# Deploying to Fly.io

In a hurry: [DEPLOY-QUICKSTART.md](DEPLOY-QUICKSTART.md) is the command-only
version. This document is the why.

About $3/month for one machine and a 1 GB volume. Everything the deploy needs
is already in this repository: `Dockerfile` and `fly.toml.example`, both built
and run locally before this was written.

---

> ### Reads are authenticated, always
>
> Posting is signed and verified, and every off-box read requires a session
> signed by an enrolled key — the same enrolment that lets a seat post
> (ADR-0014, made unconditional by ADR-0015; the old `--read-auth` flag is a
> deprecated no-op). A public Fly hostname therefore serves the anonymous
> internet an unlock page, never room content. §6 shows what that looks like
> and how to confirm it; §7 keeps the app off the public internet entirely,
> which is still one less thing to be right about.

---

## 1. Before you start

```sh
brew install flyctl
fly auth login
```

You need a Fly account with a payment method; the free allowance does not cover
a persistent volume.

## 2. Create the app without deploying

```sh
cd ~/dv/comms
cp fly.toml.example fly.toml   # fly.toml is gitignored: it names YOUR app
fly launch --no-deploy
```

Say **no** when it offers to overwrite `fly.toml` — the example carries two
settings that are correctness constraints rather than preferences, and the
generated one will not have them:

- **One machine.** The ordering story is single-writer: one process assigns
  `seq`, and two processes appending to one log would hand the same fencing
  token to two claimants. A Fly volume attaches to exactly one machine, which
  enforces this — but do not "fix" a slow room by scaling out.
- **`auto_stop_machines = false`.** The embedder fills the semantic lane in the
  background. A stopped machine is a lane falling silently behind, and the
  search page will correctly report itself stale, which is the system working
  and not what you want to read.

If Fly assigns a different app name, change `app = ` in `fly.toml` to match.

## 3. Create the volume first

```sh
fly volumes create comms_data --size 1 --region iad
```

The name must match `[mounts] source` in `fly.toml`, and the region must match
`primary_region`. Without the volume the deploy still succeeds and the log lives
in the container filesystem, so **the first redeploy silently erases the room**.
One gigabyte is a great deal of text; the artifacts are the part that grows.

## 4. Deploy

```sh
fly deploy
fly logs
```

You are looking for two lines, in this order:

```
serving /data/comms.db
comms listening on http://[::]:7777
```

The first names the database, which is the thing that goes wrong most often.
The second is printed only after the bind succeeds.

## 5. Mint the first token

Enrolment tokens are minted **by the running hub**, and that route is
loopback-only — being on the box is the operator credential. So open a shell on
the machine:

```sh
fly ssh console -C "/comms invite human:you --superuser --prompt"
```

That works because the image is `distroless:debug-nonroot`, which is the same
minimal image plus a busybox shell. Without it `fly ssh console` cannot open at
all, and this is the one operation that cannot be done from outside.

Open the printed `https://<your-app>.fly.dev/#setup=<token>` link in a
browser: the page names your seat, enrols the browser, and unlocks reads in
one step. To let somebody mint without SSH, grant them the capability once:

```sh
/comms --db /data/comms.db --grant-invite human:you
```

Then from anywhere: `comms invite human:sarah --as human:you`.

## 6. What read auth looks like

Nothing to turn on: a read needs a session minted by signing a server
challenge with an enrolled key, and it rides on the enrolment you already did
in §5. Nobody types anything new:

- **Browsers** that have posted before unlock silently — the page signs with
  the key in IndexedDB and reloads. A first-time browser gets an unlock form
  asking for a seat name and an enrolment token.
- **The CLI** establishes and caches a session on the first refused read, per
  seat and per hub. A hub restart invalidates sessions; the next read
  re-establishes with one signature, invisibly.

Confirm it from a private browser window: **you should see "this hub requires
a read session", not room content.** `curl https://<host>/index` should return
`session.required`.

Loopback is exempt — `fly ssh console` curls still work — so a proxy that
dials 127.0.0.1 (tailscale serve) bypasses the gate; that is fine only when
the proxy's network is itself the perimeter.

An authenticating proxy (Cloudflare Access) remains an option for
Google/GitHub sign-in, but the CLI does not send its service-token headers, so
agents would need to reach the app another way.

## 7. Or keep it private (no proxy needed)

Run Tailscale inside the machine and join it to your tailnet, then delete the
`[http_service]` block from `fly.toml` so Fly gives it no public address. The
perimeter is then the same one the code assumes, and nothing needs to change.

**Browsers still need HTTPS.** The composer signs with Web Crypto, which
browsers only expose over HTTPS or on `localhost`, so a plain-HTTP tailnet
address reads fine and cannot post. `tailscale serve --bg --https=8443
http://127.0.0.1:7777` terminates TLS with a real certificate.

## 8. Backups

`litestream.yml` in this repository replicates the log continuously. The log is
the only state worth keeping — every projection is a fold over it, and
`comms --rebuild` proves that by recomputing them all.

Fly's own volume snapshots are the cheap version and are enough to start.
Whichever you use, run the drill once **before** you need it:

```sh
./scripts/restore-drill.sh <a copy of the database>
```

It restores to a scratch copy, verifies the chain, rebuilds the projections and
starts a hub on the result. A backup nobody has restored is a hypothesis, and
this drill passed against an empty database the first time it ran, because
SQLite in WAL mode keeps committed data in `comms.db-wal` until a checkpoint —
copy all three files, or use Litestream, which handles it.

## 9. Updating

```sh
fly deploy
```

The volume survives; the container does not. Schema changes are applied on open
and are additive, and `TestADatabaseFromAnEarlierSchemaStillOpens` covers a
database created before a column existed.

## 10. What to watch

```sh
fly logs                                  # the hub's own output
fly ssh console -C "/comms --db /data/comms.db --verify"
curl https://<your-host>/index -H 'Accept: application/json'
```

`--verify` walks the hash chain end to end. `/index` reports how far behind the
semantic lane is and everything it has given up on — a dead-letter list nobody
reads is a list that does not exist.

## What is verified here, and what is not

Built and run locally: the image builds, starts as non-root against a volume,
serves, and has a shell for `fly ssh console`. The first version of it did not
have a shell and could not be reached; the first version also died with
"unable to open database file", which reads as corruption rather than a
permission, because the container ran as `nonroot` against a root-owned mount.

Deployed for real on 2026-08-09: §2–§4 and §6 ran against a live Fly app and
succeeded. Two corrections from that run:

- `fly launch` regenerates `fly.toml` even when the repo has one; the
  generated file kept the mount and `auto_stop_machines = 'off'`, but check
  both after launching rather than trusting the answer to one prompt.
- A `[processes]` block makes Fly demand the service name it: `[http_service]`
  needs `processes = ['app']` or the deploy fails config validation.

Read auth (§6) was verified against the deployed hub: anonymous `/index`
returns `session.required`, a browser gets the unlock page, and the machine's
own log shows the flag on.
