# syntax=docker/dockerfile:1
#
# lit-server image: a static binary (CGO_ENABLED=0) on distroless/static, run as
# a non-root user. Built locally by `docker compose build` (see compose.yaml and
# docs/installation.md); nothing is published to a registry.

ARG GO_IMAGE=golang:1.27
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG LIT_VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-X github.com/nerdwave-nick/nerdwave-workflows/internal/protocol.ReleaseVersion=${LIT_VERSION}" \
      -o /out/lit-server ./cmd/lit-server
# The mount points, owned by the runtime user. Docker copies /data's owner and
# mode into a new named volume, so lit-server can create its database there.
ARG LIT_UID=65532
ARG LIT_GID=65532
RUN mkdir -p /out/root/data /out/root/backup /out/root/export \
 && chmod 0700 /out/root/data \
 && chown -R "${LIT_UID}:${LIT_GID}" /out/root

FROM ${RUNTIME_IMAGE}
ARG LIT_UID=65532
ARG LIT_GID=65532
COPY --from=build /out/lit-server /lit-server
COPY --from=build /out/root/ /
USER ${LIT_UID}:${LIT_GID}
ENV LIT_DATA_DIR=/data
ENTRYPOINT ["/lit-server"]
