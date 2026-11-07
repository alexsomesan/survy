# survy image: built locally with docker, loaded on the router with podman.
#   make image    -> localhost/dash:<git short sha> and localhost/dash:latest

# --- 1. Frontend -> web/dist ---------------------------------------------------
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- 2. Go binary (static, no cgo) with web/dist embedded -----------------------
FROM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# The embed in web/embed.go reads web/dist, so it must be in place before go build.
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w' -o /out/survy ./cmd/survy

# --- 3. Runtime ---------------------------------------------------------------
# busybox (static, musl) instead of scratch only for `nice` and `ionice`: VyOS
# has no container option for scheduling priority, so the entrypoint drops to
# the lowest CPU (nice 19) and I/O (idle class) priority before exec'ing survy,
# which ends up as PID 1. Lowering one's own priority needs no capability.
FROM busybox:1.37-musl
COPY --from=go /out/survy /usr/local/bin/survy
ENV DASH_LISTEN=192.168.2.4:80,10.0.0.1:80 \
    DASH_ROOT=/host \
    DASH_DATA=/data
VOLUME /data
ENTRYPOINT ["nice", "-n", "19", "ionice", "-c", "3", "/usr/local/bin/survy"]
