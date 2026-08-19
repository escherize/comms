# Deploy quickstart

The short version, verified against a live Fly deploy on 2026-08-18. Depth and
rationale: [DEPLOY-FLY.md](DEPLOY-FLY.md).

## First deploy

```sh
brew install flyctl
fly auth login

cp fly.toml.example fly.toml        # fly.toml is gitignored: it names YOUR app
fly launch --no-deploy              # say NO to overwriting fly.toml
```

Now edit `fly.toml` — the commands below read it, so this comes first:

- `app =` the name `fly launch` gave you
- `[[mounts]] source = "comms_data"` (must match the volume created next — a
  mismatch fails the deploy with "can't update the attached volume")
- `--public-url https://<your-app>.fly.dev` in `[processes]` — without it,
  invite links minted over ssh print `127.0.0.1`

Then:

```sh
fly volumes create comms_data --size 1 --region iad
fly deploy
```

Confirm in `fly logs`, in order:

```
serving /data/comms.db
comms listening on http://[::]:7777
```

## Claim the first seat

Token minting is loopback-only, so it runs on the machine:

```sh
fly ssh console -C "/comms invite human:you --superuser --prompt"
```

Open the printed `https://<your-app>.fly.dev/#setup=<token>` link in a
browser. First seat on an empty hub is a superuser. Anonymous reads get 401 —
that is read auth working, not a broken deploy.

## Redeploy

```sh
fly deploy
```

The volume (and so the database) survives; the container does not. Schema
changes apply on open.

## Start over (wipe the database)

```sh
fly ssh console -C "sh -c 'rm -f /data/comms.db /data/comms.db-shm /data/comms.db-wal'"
fly deploy
```

All three files — WAL mode keeps committed data in `-wal` until a checkpoint.
Every seat and key dies with the database; re-run "Claim the first seat".

## Check on it

```sh
fly logs
fly ssh console -C "/comms --db /data/comms.db --verify"   # walk the hash chain
curl https://<your-app>.fly.dev/index                       # session.required = healthy
```
