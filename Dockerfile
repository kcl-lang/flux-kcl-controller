ARG GO_VERSION=1.22

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} as build

COPY / /src
WORKDIR /src

# The TARGETOS and TARGETARCH args are set by docker. We set GOOS and GOARCH to
# these values to ask Go to compile a binary for these architectures. If
# TARGETOS and TARGETOS are different from BUILDPLATFORM, Go will cross compile
# for us (e.g. compile a linux/amd64 binary on a linux/arm64 build machine).
ARG TARGETOS
ARG TARGETARCH

# build
ENV CGO_ENABLED=0
RUN go build -a -o kcl-controller cmd/main.go

FROM kcllang/kcl

RUN apt-get update && apt-get install -y ca-certificates tini

COPY --from=build /src/kcl-controller /usr/local/bin/

# RUN addgroup -S controller && adduser -S controller -G controller
RUN groupadd controller && useradd -g controller controller

USER controller

ENTRYPOINT [ "/usr/bin/tini", "--", "kcl-controller" ]
