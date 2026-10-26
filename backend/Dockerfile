FROM golang:1.25.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/blob-migrate ./cmd/blob-migrate
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/store-migrate ./cmd/store-migrate
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/budget ./cmd/budget

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backup ./cmd/backup
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/model-key-rotate ./cmd/model-key-rotate

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg fonts-noto-cjk curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 studio && useradd --uid 10001 --gid studio --no-create-home studio \
    && mkdir -p /app/data && chown studio:studio /app/data
COPY --from=build /out/server /app/server
COPY --from=build /out/worker /app/worker
COPY --from=build /out/blob-migrate /app/blob-migrate
COPY --from=build /out/store-migrate /app/store-migrate
COPY --from=build /out/budget /app/budget
COPY --from=build /out/backup /app/backup
COPY --from=build /out/model-key-rotate /app/model-key-rotate
WORKDIR /app
ENV HTTP_ADDR=0.0.0.0:8080 DATA_DIR=/app/data
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD curl --fail --silent http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/server"]
