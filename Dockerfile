# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27.2
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie AS build

WORKDIR /src
COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} VERSION=${VERSION} ./build.sh build \
    && mkdir -p /out/data /out/tmp \
    && cp bin/wren /out/wren

FROM scratch

ARG UID=1000
ARG GID=1000

COPY --from=build /out/wren /wren
COPY --from=build --chown=${UID}:${GID} /out/data /data
COPY --from=build --chown=${UID}:${GID} /out/tmp /tmp

ENV XDG_DATA_HOME=/data
USER ${UID}:${GID}
EXPOSE 8080

ENTRYPOINT ["/wren"]
CMD ["--addr", "0.0.0.0:8080", "--db", "/data/wren.db"]
