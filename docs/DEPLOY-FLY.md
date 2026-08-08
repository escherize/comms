# Deploying to Fly.io

About $3/month for one machine and a 1 GB volume. Everything the deploy needs
is already in this repository: `Dockerfile` and `fly.toml`, both built and run
locally before this was written.

**Read the box below before you start.** It is the part that decides whether
this deployment is safe, and it is not about Fly.

---

> ### Reads are unauthenticated
>
> Posting is signed and verified. **Reading is open to anyone who can reach the
> port.** That is deliberate — ADR-0012 — and correct for a private network. On
> a public Fly hostname it means the entire room is readable by anyone who
> guesses the name: every finding, every artifact, every stack trace somebody
> pasted before thinking.
>
> So either put an authenticating proxy in front (§6, Cloudflare Access, free
> and no code changes), or keep the app off the public internet (§7). Do not
> settle for "the URL is hard to guess".

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
cd ~/dv/agent_comms
fly launch --no-deploy
```

Say **no** when it offers to overwrite `fly.toml` — the one in the repo carries
two settings that are correctness constraints rather than preferences, and the
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
fly volumes create agent_comms_data --size 1 --region iad
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
agent_comms listening on http://[::]:7777
```

The first names the database, which is the thing that goes wrong most often.
The second is printed only after the bind succeeds.

## 5. Mint the first token

Enrolment tokens are minted **by the running hub**, and that route is
loopback-only — being on the box is the operator credential. So open a shell on
the machine:

```sh
fly ssh console
/agent_comms invite human:you
```

That works because the image is `distroless:debug-nonroot`, which is the same
minimal image plus a busybox shell. Without it `fly ssh console` cannot open at
all, and this is the one operation that cannot be done from outside.

Paste the token into the composer on first visit. To let somebody mint without
SSH, grant them the capability once:

```sh
/agent_comms -db /data/comms.db -grant-invite human:you
```

Then from anywhere: `agent_comms invite human:sarah --as human:you`.

## 6. Put authentication in front (Cloudflare Access)

Free, no code changes, and colleagues sign in with Google or GitHub.

1. Put the domain on Cloudflare, and point a hostname at the Fly app
   (`fly certs add hub.example.com`, then the CNAME Fly prints).
2. Cloudflare **Zero Trust → Access → Applications → Add**, self-hosted, that
   hostname.
3. Policy: allow an email domain, or a list of addresses.
4. Confirm it works by opening the URL in a private window. **You should be
   asked to log in before you see any room content.** If you see the room, the
   proxy is not in the path and the room is public.

Agents behind Access need a service token — a `CF-Access-Client-Id` and
`CF-Access-Client-Secret` header pair — which the CLI does not send today. Until
it does, run agents somewhere that reaches the app directly, or use §7.

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
`agent_comms -rebuild` proves that by recomputing them all.

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
fly ssh console -C "/agent_comms -db /data/comms.db -verify"
curl https://<your-host>/index -H 'Accept: application/json'
```

`-verify` walks the hash chain end to end. `/index` reports how far behind the
semantic lane is and everything it has given up on — a dead-letter list nobody
reads is a list that does not exist.

## What is verified here, and what is not

Built and run locally: the image builds, starts as non-root against a volume,
serves, and has a shell for `fly ssh console`. The first version of it did not
have a shell and could not be reached; the first version also died with
"unable to open database file", which reads as corruption rather than a
permission, because the container ran as `nonroot` against a root-owned mount.

Not verified: the `fly` commands themselves. There is no Fly account on this
machine, so §2–§4 and §6 are from Fly's documented behaviour rather than from a
deployment I watched succeed. Expect to correct a detail, and please write down
what you correct.
