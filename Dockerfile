# The public demo page, as one static binary in a base image with no shell.
#
# It builds cmd/rzp-demo and nothing else. The other two binaries in this
# repository take Razorpay credentials; this one cannot, and shipping only this
# one is the first half of that guarantee. The second half is
# cmd/rzp-demo/safety_test.go, which fails the build if the package ever reads
# an environment variable other than PORT or constructs a gateway client.
#
# There is no ARG and no ENV carrying a secret, deliberately. The image needs
# none: the page, the committed run artifacts, and the fixture book are all
# compiled into the binary with go:embed, so the container reads no file and
# opens no outbound connection after it starts.

FROM golang:1.25-alpine AS build

WORKDIR /src

# The module files first, so a change to the source does not re-download the
# module cache on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off and a static link, because the runtime stage has no libc.
# -trimpath keeps the build machine's paths out of the binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/rzp-demo \
    ./cmd/rzp-demo

# distroless static: no shell, no package manager, no busybox. Nothing in the
# image can be run except the binary, which is the point on a public host.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/rzp-demo /rzp-demo

# PORT is the one variable this binary reads. A host that sets its own overrides
# this; a laptop that sets nothing gets 8080 either way.
ENV PORT=8080
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/rzp-demo"]
