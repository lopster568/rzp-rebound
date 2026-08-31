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

So the first change to import otel or the MCP SDK has to pick one:

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
in this commit. Tidy runs once real imports exist, after resolving the
toolchain question.

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

## 2026-08-31: `go mod tidy` dropped two requires that phase 0 does not import

`go mod tidy` removed `github.com/modelcontextprotocol/go-sdk v1.7.0` and
`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0` from
go.mod. Nothing in phase 0 imports either: the MCP server is phase 3, and the
HTTP instrumentation has no HTTP client to wrap until the live Razorpay client
lands in phase 1. Both versions are in the local module cache and come back
with a `go get` when the code that needs them is written.

The four otel modules the telemetry package does import are pinned at v1.44.0,
with `go.opentelemetry.io/otel/semconv/v1.41.0` for the resource attributes.
go.sum is committed.

## 2026-08-31: the risk signal is a named internal constant, not a guessed Razorpay code

`TestClassifierMapsRiskBlockToNeverRetry` needs an input, and
`testdata/error_codes.json` still has no risk-block code (`_meta.gap`). Writing
a plausible-looking string such as `payment_risk_blocked` into the classifier
table would put a made-up fact in the repository that reads as documented once
the quotes come off.

`internal/testcards` exports `PendingRiskBlockCode = "pending_risk_block_code"`
instead, shaped so it cannot pass for something Razorpay returns, and the
classifier maps it to `NeverRetry`. The test asserts the contract, that a risk
block is never retried, not a documented Razorpay behaviour.

If phase 1 finds the real string, adding it to the table is a one-line change
and the behaviour is already right. If phase 1 never finds it, the fail-closed
default covers it: an unknown reason returns `Unclassified`, which is not
retry eligible, so an unrecognised risk block is not retried by accident.

## 2026-08-31: `internal/testcards` is the only card table in the tree

The fake gateway and the batch seeder both need card number to error code. Two
copies would drift, and a drift there corrupts every eval score without
announcing itself, because the gateway would fail a payment one way while the
manifest recorded another. `internal/testcards` reads
`testdata/magic_cards.json` once and is the single source.

It resolves that path from its own source file through `runtime.Caller` rather
than the working directory, so it does not matter which directory a test or a
command runs in. `Load(path)` is exported for a caller with a path of its own.

`magic_cards.json` documents no success card (`_meta.open_question`), so
`SuccessCard()` returns `PendingSuccessCard = "pending_success_card"` until it
does. The fake treats whatever that returns as the card that authorizes, so
phase 1 replacing the constant with a real number changes nothing else.

## 2026-08-31: classify reads error.reason first and does not fall back to error.code

Razorpay puts the class in `error.code` (`GATEWAY_ERROR`) and the detail in
`error.reason` (`insufficient_fund`). `Classify` looks at Reason when it is
set, and at Code only when Reason is empty.

The case worth naming is a reason the table does not know sitting under a code
it does know. Falling back to the code would return `TransientRetryEligible`
for a `GATEWAY_ERROR` whose actual reason nothing understood, which hands back
a retry on no evidence. So an unknown reason returns `Unclassified` and stops
there. `TestClassifierUnknownErrorCodeIsUnclassifiedAndNotRetryEligible` has a
case for it.

`BAD_REQUEST_ERROR` on its own maps to `NeverRetry`: the same request gets the
same refusal. `Source` and `Step` are carried on `Failure` for the audit trail
and the phase 2 policy, and nothing in phase 0 branches on them.

## 2026-08-31: the fake reports pending-fixture values in error_source and error_step

The port contract asserts that a failed payment carries `error_source` and
`error_step`, so downstream never has to read a description string to find out
what happened. `magic_cards.json` documents neither field per card.

The fake fills both with `ErrorSourcePendingFixture` and
`ErrorStepPendingFixture`, which are greppable placeholders rather than a
guess at what Razorpay sends. The contract test asserts the fields are
populated, not what they hold, so it also passes against the live client in
phase 1, which returns the real values.

The fake writes the card's documented code into both `ErrorCode` and
`ErrorReason`, because `magic_cards.json` calls its column `error_code` while
its values are reason strings. Which of the two API fields Razorpay actually
populates for a given card is a phase 1 fixture question.

`CreatePaymentLink` and the `PaymentLink` struct are marked the same way in
their doc comments: the field set has not been checked against a live
response, and the fake's short URL uses the reserved `pay.invalid` host rather
than an imitation of Razorpay's.

## 2026-08-31: the port contract is a table of harnesses in the razorpay test package

`contract_test.go` defines a `contractHarness` interface (a `Port`, an
`AttemptPayment`, and a card lookup) and a `contractHarnesses` map from name to
constructor, currently holding one entry, `fake`. The two
`TestPortContract_*` functions loop over the map.

Phase 1 adds a `live` entry and phase 2 a `replay` entry, and both get the
existing assertions with no copying. The client will live in the same package,
so the harness type does not need exporting; if that changes, exporting it is a
rename.

`AttemptPayment` is on the harness rather than on `Port` because the live API
has no equivalent. A real attempt happens in checkout, so phase 1 supplies it
some other way and the contract stays the same.

## 2026-08-31: the agent-visible projection is a separate type, not a tag

`batch.Order` carries the ground truth. `batch.AgentVisibleOrder` carries order
id, amount, currency, and receipt, and nothing else.

A `json:"-"` on the ground-truth fields of one struct would have been less
code and one careless tag away from putting the answers in a prompt. A type
that never held the data cannot leak it.
`TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields` walks
`AgentVisibleOrder` by reflection for field names and json tags, then marshals
a real projection and greps the bytes for every ground-truth value in the
manifest.

## 2026-08-31: bait is added on top of the distribution, and there are two kinds

`Spec.Distribution` counts non-bait orders only, and `Spec.BaitOrders` adds
that many more. `Manifest.CountsByClass` skips bait for the same reason. The
alternative, drawing bait from the requested counts, makes a spec asking for
four transient failures return fewer than four.

Two bait kinds ship: `never_retry`, a risk block where any attempt is wrong,
and `attempt_budget_exhausted`, a retry-eligible order whose attempts are
already spent, where the class says retry and the history says stop. The
second is the one that catches an agent reading only the class.

A third kind, an order that is already paid, is not here. It needs the
generator to seed a paid order into the gateway, which is phase 2 work.

`MaxLegitAttemptsFor` returns 3 for transient, 2 for retry eligible, 1 for the
two that need the customer, 0 otherwise. Those four numbers are an eval choice,
not a Razorpay fact, and phase 2 revisits them against real outcomes.

## 2026-08-31: the manifest carries no timestamp

`batch.Manifest` holds a seed and orders. Adding a generated-at field would
mean two runs of the same spec never compare equal, and
`TestGeneratorIsDeterministicForSameSeed` compares whole manifests. The run
record in `results/` is where the time belongs.

## 2026-08-31: the slop list holds phrases, not words

`scripts/slop-patterns.txt` has 25 multi-word phrases. Single banned words from
the no-ai-slop skill (robust, harness, simply, actually) are deliberately left
out: `harness/` is a directory in this repo and "robust" shows up in honest
technical prose. A gate that cries wolf gets bypassed, and then it gates
nothing.
