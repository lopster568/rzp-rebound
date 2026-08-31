# Phase 0 decisions

Append an entry when a choice would otherwise have to be reverse-engineered
later. Date every entry.

## 2026-08-31: go.mod says `go 1.24`, but every dependency needs 1.25

The installed toolchain is go1.24.6 and the plan pins `go 1.24`. All six
required modules declare `go 1.25.0` in their own go.mod:
`github.com/modelcontextprotocol/go-sdk v1.7.0`, `go.opentelemetry.io/otel
v1.44.0`, its `sdk`, both exporters, and
`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0`.

Right now nothing imports them, and module graph pruning means the build never
reads their go.mod files, so `go build ./...`, `go vet ./...`, and `go test
./...` all pass. The conflict appears on the first import: Go will try to
switch to a 1.25 toolchain, and `GOPROXY=off go mod download` already fails
with `requires go >= 1.25.0 (running go 1.24.6)`.

So the first agent to import otel or the MCP SDK has to pick one:

1. Install go1.25 and set `go 1.25` in go.mod and `go-version: "1.25.x"` in
   `.github/workflows/ci.yml`.
2. Leave the toolchain at 1.24.6 and let `GOTOOLCHAIN=auto` download a 1.25
   toolchain. Cached 1.25 toolchains already exist under
   `~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.*`, but CI would download
   one on every run.
3. Drop to dependency versions whose go directive is 1.24 or lower. Nothing
   in the local module cache qualifies: go-sdk v1.5.0 and v1.6.1 also say
   `go 1.25.0`.

Option 1 is the honest one. It was not taken here because the brief for this
phase said not to write 1.25.

## 2026-08-31: no `go mod tidy` yet, and no go.sum

The six requires are written into go.mod by hand and left there. `go mod tidy`
would delete all of them, since no file imports anything yet, and `go mod
download` fails for the toolchain reason above. There is therefore no go.sum
in this commit. The implementation agent runs tidy once real imports exist,
after resolving the toolchain question.

## 2026-08-31: cmd binaries are stubs that exit 1

`cmd/rzp` and `cmd/rzp-mcp` contain a `main()` that prints a phase 0 notice and
exits 1. A `package main` with only a doc comment breaks `go build ./...` with
`function main is undeclared in the main package`, which would leave CI red
from the first commit.

## 2026-08-31: the compose file runs jaeger with its default config

Copied from `~/jaeger-mcp-bench/compose/docker-compose.yml`, jaeger service
only. Dropping prometheus meant dropping `METRICS_STORAGE_TYPE` and the
`PROMETHEUS_*` variables that pointed at it, and dropping the
`jaeger-config.yml` mount and `--config` flag, since that file belonged to the
bench. The container name is `rzp-jaeger` so it does not collide with a bench
container on the same machine.

## 2026-08-31: the classify test list names a risk block that the docs table does not

`TestClassifierMapsRiskBlockToNeverRetry` is in the phase 0 test list, but the
Razorpay test-card table has no risk-block card and no risk-block code, so
`testdata/error_codes.json` does not list one. The gap is recorded in that
file's `_meta.gap`. Phase 1 confirms the real code string from the Razorpay
docs and adds it. Until then that test has to invent its input, which means it
tests the classifier's contract, not a documented Razorpay behaviour.

## 2026-08-31: resolved the toolchain conflict by bumping go.mod to `go 1.25.0`

Taken: option 1 from the entry above, with the twist that no system-wide go1.25
install was needed. `go.mod` now says `go 1.25.0` and
`.github/workflows/ci.yml` asks for `go-version: "1.25.x"`. The system go is
still go1.24.6, `GOTOOLCHAIN` is the default `auto`, and
`golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64` was already in the module
cache, so the go command switches itself.

Evidence, run in the repo root after the edit:

```
$ go version
go version go1.25.0 linux/amd64
$ go build ./...
$ go vet ./...
$ GOPROXY=off go mod download go.opentelemetry.io/otel go.opentelemetry.io/otel/sdk \
    go.opentelemetry.io/otel/exporters/stdout/stdouttrace \
    go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
```

All four exit 0. `GOPROXY=off` proves the modules and the 1.25 toolchain both
come from the local cache, not the network. `go version` reporting 1.25.0 while
the installed binary is 1.24.6 is the automatic switch doing its job.

CI on `ubuntu-latest` gets a 1.25 toolchain from `actions/setup-go`, so the
runner never pays for a toolchain download at build time.

## 2026-08-31: the slop list holds phrases, not words

`scripts/slop-patterns.txt` has 25 multi-word phrases. Single banned words from
the no-ai-slop skill (robust, harness, simply, actually) are deliberately left
out: `harness/` is a directory in this repo and "robust" shows up in honest
technical prose. A gate that cries wolf gets bypassed, and then it gates
nothing.
