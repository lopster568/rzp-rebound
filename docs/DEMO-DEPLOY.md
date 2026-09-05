# Putting the demo page on a public link

`cmd/rzp-demo` is the public face of this repository: one Go binary, one page,
two views. It is what goes in the form field that wants a working link.

Three ways to serve it, in the order you should reach for them. Every one is a
single command or a fixed set of clicks.

## What is behind the link

Nothing that can spend money.

The binary reads one environment variable, `PORT`. It holds no Razorpay
credential, imports no `internal/config`, never constructs a gateway client, and
makes no outbound request of any kind.
`cmd/rzp-demo/safety_test.go` holds all three of those as assertions over the
package's own syntax tree, so a change that broke one would fail `make ci`
rather than reaching a public host quietly.

The replay view serves committed files from the 2026-09-05 run against Razorpay
test mode. The run view drives a committed fixture book through the real
detectors, the real gate, and the real intervention engine, with an in-memory
gateway on the far side. A payment link the run mints points at `pay.invalid`,
which resolves nowhere, so nothing a visitor clicks reaches a checkout.

## 1. Render, for the link you paste in a form

Render reads `render.yaml` at the repository root, so there is no service
configuration to fill in.

1. Push this repository to GitHub, if it is not there already.
2. Open <https://dashboard.render.com/blueprints> and click **New Blueprint
   Instance**.
3. Pick this repository. Render finds `render.yaml` and shows one web service
   called `rzp-rebound`.
4. Click **Apply**. Leave every field alone. There is no environment variable to
   set and no secret to paste.
5. Wait for the first build. It is a Docker build of `./Dockerfile`, which
   compiles `cmd/rzp-demo` and copies the binary into a distroless image.
6. Render marks the service live once `GET /healthz` answers `200`. The URL on
   the service page is the link.

Free instances sleep when nothing has hit them for a while, and the first
request after a sleep takes a few seconds to wake the container. Open the link
once yourself before you hand it to anyone.

To confirm a deployment from a terminal, with `YOUR-SERVICE` replaced by the
subdomain Render assigned:

```
curl -s https://YOUR-SERVICE.onrender.com/healthz
```

A healthy answer names the service, the committed run being replayed, and the
number of items in the fixture book.

## 2. A cloudflared tunnel, for a link right now

This needs no account and no deployment. It publishes whatever is running on
your machine, and the link dies when you close the terminal.

In one terminal:

```
go run ./cmd/rzp-demo
```

In a second terminal:

```
cloudflared tunnel --url http://localhost:8080
```

`cloudflared` prints a `https://<something>.trycloudflare.com` URL. That is the
link. It is the same binary and the same two views as the Render deployment, so
nothing about the page changes.

Install `cloudflared` from
<https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/>
if it is not on the machine. A quick tunnel is anonymous and rate limited, and
Cloudflare does not promise it will stay up, so use it to show somebody the page
while you watch and use Render for a link that has to work on its own at
02:00.

## 3. Locally, to look at it yourself

```
go run ./cmd/rzp-demo
```

Then open <http://localhost:8080>. `PORT` overrides the port:

```
PORT=9000 go run ./cmd/rzp-demo
```

Or through the Makefile, which is the same command:

```
make demo-web
```

To check it without a browser:

```
curl -s localhost:8080/healthz
curl -sN localhost:8080/api/run | head -20
```

The second one runs the engine and streams the decisions back as server-sent
events, which is exactly what the page reads.

## The Docker image, if you would rather run that

```
docker build -t rzp-rebound .
docker run --rm -p 8080:8080 rzp-rebound
```

The image is `gcr.io/distroless/static-debian12:nonroot`: no shell, no package
manager, and a non-root user. Nothing in it can be executed except the binary.

Do not pass credentials to it. There is no `--env-file` line above because the
binary has nothing to do with a key, and adding one would put a secret in the
environment of a process with no code path to reach it.

## What the two views show

**Replay a real run.** The committed artifacts of one run against Razorpay test
mode on 2026-09-05: the seeded book of receivables, the sightings the three
detectors returned and the items left after the dedupe collapsed the ones that
are the same debt, the verdicts by rule with the escalation decisions counted
separately, every result row, the append-only audit ledger, the escalation
queue, and the delta between the two snapshots taken around a payment. The
recovered figure is not copied out of a log. The page computes it at startup by
calling `riskrun.Diff` over the two committed snapshot files, which is the same
function `cmd/rzp risk-poll` calls, and it carries its `n=1` label on screen.

**Run the engine.** The real pipeline, in the server process, streamed to the
page as it happens: the three detectors, `detect.Collapse`, the fifteen-rule
gate, and the intervention engine. Every row it writes is stamped `simulated`,
because the detectors read a fixture book instead of an account and the
intervention engine calls a map instead of Razorpay. The endpoint runs one
engine at a time and answers `429` with a `Retry-After` to a second concurrent
request, which the page turns into a line telling the visitor to try again.

## If something is wrong

| What you see | What it is |
|---|---|
| Render says the service is unhealthy | `/healthz` did not answer `200`. Read the deploy log: the only way this fails is a build failure, since the health check touches nothing but memory. |
| The page loads and the replay panels stay empty | `GET /api/replay` failed. Open it directly in the browser to read the error. |
| The run button says the stream ended | Somebody else is running the engine. The endpoint runs one at a time on purpose. Press it again. |
| The first request after a while takes ten seconds | A free Render instance woke up. It stays warm afterwards. |
| The tunnel URL stopped working | A `cloudflared` quick tunnel lives as long as the process. Restart it and the URL changes. |
