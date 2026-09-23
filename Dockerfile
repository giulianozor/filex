# Stage 1: build
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# .git is excluded by .dockerignore, so `git describe` inside the build would
# always yield "dev". Take the version from a build arg instead (the Makefile
# passes the same `git describe` value it uses for local builds).
ARG VERSION=dev
RUN go build -ldflags "-X main.Version=${VERSION}" -o /filex ./cmd/filex
RUN go build -o /filex-passwd ./cmd/filex-passwd

# Stage 2: minimal runtime image
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata unzip p7zip unrar ffmpeg

COPY --from=builder /filex /usr/local/bin/filex
COPY --from=builder /filex-passwd /usr/local/bin/filex-passwd

RUN mkdir -p /data /etc/filex

# Personal preferences (favourites, protected paths, show_dotfiles, wipe
# settings, password hashes) live in each configured user's own
# ~/.config/filex.yaml, resolved from the account database — never the home of
# the root daemon. In no-auth mode (or for users without a passwd entry) the
# process user's file is used. Alpine does not set HOME, so without an explicit
# value that no-auth/global fallback would resolve through an undocumented
# location; pin it so a mounted config volume can target the canonical location
# (docker-compose mounts it at /root/.config/filex.yaml).
ENV HOME=/root

# The server resolves a relative base_path against the process working
# directory; make the jail anchor explicit and writable so configs that omit
# base_path do not default to the container root (/) — the one path the jail
# must never allow itself to serve.
WORKDIR /data

EXPOSE 8080

# /api/config is behind the auth middleware and would return 401, falsely
# marking a healthy server as unhealthy; /api/version is public.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/api/version || exit 1

ENTRYPOINT ["filex"]
CMD ["-config", "/etc/filex/config.yaml"]
