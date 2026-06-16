# warble Go AI server. CGO is required: gopkg.in/hraban/opus.v2 binds libopus.
FROM golang:1.26-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
        libopus-dev libopusfile-dev pkg-config && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -o /out/server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libopus0 libopusfile0 ca-certificates curl && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/server /app/server
# config/ (prompt.txt + runtime.json) is mounted at runtime; see docker-compose.yml.
EXPOSE 8000 8003
ENTRYPOINT ["/app/server"]
