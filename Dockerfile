# We use the Go 1.23 version unless asked to use something else.
# The GitHub Actions CI job sets this argument for a consistent Go version.
ARG GO_VERSION=1.23
ARG BASE_IMAGE=kcllang/kcl

# Setup the base environment. The BUILDPLATFORM is set automatically by Docker.
# The --platform=${BUILDPLATFORM} flag tells Docker to build the function using
# the OS and architecture of the host running the build, not the OS and
# architecture that we're building the function for.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} as build

COPY / /src
WORKDIR /src

ENV CGO_ENABLED=0

# We run go mod download in a separate step so that we can cache its results.
# This lets us avoid re-downloading modules if we don't need to. The type=target
# mount tells Docker to mount the current directory read-only in the WORKDIR.
# The type=cache mount tells Docker to cache the Go modules cache across builds.
RUN --mount=target=. --mount=type=cache,target=/go/pkg/mod go mod download

# The TARGETOS and TARGETARCH args are set by docker. We set GOOS and GOARCH to
# these values to ask Go to compile a binary for these architectures. If
# TARGETOS and TARGETOS are different from BUILDPLATFORM, Go will cross compile
# for us (e.g. compile a linux/amd64 binary on a linux/arm64 build machine).
ARG TARGETOS
ARG TARGETARCH

# Build the function binary. The type=target mount tells Docker to mount the
# current directory read-only in the WORKDIR. The type=cache mount tells Docker
# to cache the Go modules cache across builds.
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -o kcl-controller cmd/main.go

FROM ${BASE_IMAGE} as image
RUN apt-get update && apt-get install -y ca-certificates tini
COPY --from=build /src/kcl-controller /usr/local/bin/
# RUN addgroup -S controller && adduser -S controller -G controller
RUN groupadd controller && useradd -g controller controller

ENTRYPOINT [ "/usr/bin/tini", "--", "kcl-controller" ]
