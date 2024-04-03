ARG GO_VERSION=1.22
ARG XX_VERSION=1.2.1

FROM --platform=$BUILDPLATFORM tonistiigi/xx:${XX_VERSION} AS xx

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine as builder

# Copy the build utilities.
COPY --from=xx / /

ARG TARGETPLATFORM

WORKDIR /workspace

# copy modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache modules
RUN go mod download

# copy source code
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/controller/ internal/controller/

# build
ENV CGO_ENABLED=0
RUN xx-go build -a -o kcl-controller cmd/main.go

FROM kcllang/kcl
# FROM alpine:3.19

# RUN apk add --no-cache ca-certificates tini
RUN apt-get update && apt-get install -y ca-certificates tini

COPY --from=builder /workspace/kcl-controller /usr/local/bin/

# RUN addgroup -S controller && adduser -S controller -G controller
RUN groupadd controller && useradd -g controller controller

USER controller

ENTRYPOINT [ "/usr/bin/tini", "--", "kcl-controller" ]
