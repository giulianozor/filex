# filex

A fast, self-hosted web-based file browser with a dark theme, built in Go. Zero external runtime dependencies.

## Features

- 📁 Directory listing with icons, size, and modification time
- 📤 Multi-file upload manager (drag-and-drop or click), with per-file progress, speed, ETA, cancel, and popup view
- 📥 File download
- ✏️ In-browser text editor
- 🖼️ Inline previews for images, video, audio, and text files
- 🎬 Video editor — thumbnail timeline with navigable frames, set start/end marks, and extract multiple intervals as clips
- ✂️ Lossless clipping — intervals are cut and concatenated with ffmpeg stream copy (`-c copy`, no re-encoding), streamed with live progress
- ⏱️ Per-video interval presets — start/end marks are saved next to the video and restored when you reopen it
- 🖼️ Thumbnail cache — one-click thumbnail generation/regeneration, cancellation, and batch thumbnail creation for selected videos
- 📂 Create folders, rename, delete (single or bulk), move files
- 🛡️ Configurable protected paths to prevent deleting important files/folders
- ⭐ Configurable favourite folders (sidebar bookmarks)
- 👁️ Toggle hidden (dot) files
- 🔒 Path jail — each user is confined to their own base directory
- 🔐 Per-user login — users configured in `config.yaml` with bcrypt passwords
- 👤 Per-user UID/GID — uploaded/created files are chowned to the user's uid/gid
- 🌑 Dark, high-contrast UI with mobile-responsive layout
- 📦 Single binary with embedded assets (no external dependencies at runtime)

## Quick Start

```bash
# Build
make build

# Run (serves current directory on :8080, no auth)
./bin/filex

# Run with a config file (auth enabled when users: is defined)
./bin/filex -config config.yaml
```

## Configuration (`config.yaml`)

Instead of writing the config from scratch you can generate a sample (with or
without auth) via the bundled tool or the Makefile targets. Both refuse to
overwrite an existing file silently — the current config is backed up to
`<path>.bak.<timestamp>` first:

```bash
# No-auth sample (single shared namespace, no login)
./bin/filex -sample-config config.sample.yaml
# or
make sample-config

# Auth sample (one sample user jail, login required)
./bin/filex -sample-config-auth config.sample.yaml
# or
make sample-config-auth

# Set a real password for the sample user before running (see below)
bin/filex-passwd -user alice
```

### Where the two configuration files live

`config.yaml` (passed with `-config`, e.g. `--config /etc/filex/config.yaml`)
holds the **serving and directory settings**: host/port, session TTL, thumbnail
options, the global `base_path` and its global `show_dotfiles` default — and, in
multi-user mode, each user's `base_path` jail plus optional `uid`/`gid`. It can
stay read-only and root-managed.

Everything a user edits from the UI — and the password hash — is stored
separately in that user's own preferences file `<home>/.config/filex.yaml`,
where `<home>` is the user's home from the account database (not the home of
the process the server runs as). A root-managed daemon therefore never writes
preferences into `~root`:

```yaml
# /home/alice/.config/filex.yaml — written by the server and by filex-passwd
deletion:               # global (no-auth) secure-delete preference
  default_wipe: false
  wipe_method: fast
users:
  - username: alice
    password_hash: "$2a$12$..."   # only via filex-passwd
    show_dotfiles: true           # per-user; nil = use the server default
    protected_paths:
      - /documents/keep
    favourites:
      - name: "Home"
        path: "/"
    deletion:           # per-user secure-delete preference
      default_wipe: true
      wipe_method: zero
```

In multi-user mode every configured user gets their own copy of that file
under their own home. The per-user path is resolved by username, falling back
to the configured `uid`, through the account database (`/etc/passwd`); a user
with no account entry can be pointed at an explicit file (see the `-config`
path below). Because the file may contain password hashes it is written `0600`
and atomically (temp file + rename), exactly like the server config. On every
server or `filex-passwd` write the file is chowned to that user's `uid`/`gid`
(configured values win; otherwise the account database is used), so a root
daemon never leaves a user's own preferences file owned by root.

### No-auth mode (single user, no login required)

```yaml
host: "0.0.0.0"
port: 8080
base_path: "/data"
show_dotfiles: false
session_ttl_days: 30   # session lifetime in days (default: 30)
protected_paths:
  - "/important"
  - "/important/keep.txt"
favourites:
  - name: "Home"
    path: "/"
```

In no-auth mode `protected_paths:` and `favourites:` stay in `config.yaml`
(there is no per-user identity to attach them to); the remembered secure-delete
preference lives in the preferences file of the user the server runs as.

### Multi-user mode (login required)

When `users:` is defined, a login page is shown and each user gets their own
path jail and optional UID/GID for file ownership. `config.yaml` keeps only the
directory settings:

```yaml
host: "0.0.0.0"
port: 8080
show_dotfiles: false
session_ttl_days: 30   # session lifetime in days (default: 30)

users:
  - username: "alice"
    base_path: "/data/alice"
    uid: 1001                     # omit to skip chown
    gid: 1001
  - username: "bob"
    base_path: "/data/bob"
```

Each user's favourites, protected paths, show_dotfiles override and remembered
secure-delete choices are recorded under `users:` in their own
`<home>/.config/filex.yaml` as they change from the UI — never in `config.yaml`.

> **Note**: when `uid` or `gid` is set for any user, filex uses `setreuid`/`setregid`
> to run all file-system operations with that user's credentials.  This requires the
> server to be started as **root** (or to hold `CAP_SETUID`/`CAP_SETGID`).  If the
> server is not root and uid/gid are configured, every file-system call will fail with
> a *"setegid: operation not permitted"* error after login.  Omit `uid`/`gid` from
> the config (or start as root) to avoid this.

### Generating password hashes

`filex-passwd` writes the hash into that user's own preferences file:
`-user` targets `<home>/.config/filex.yaml` (resolved from the account
database, so the hash lands where the server reads it), `-prefs` overrides
the file path.

```bash
# Print the hash for use in config.yaml
bin/filex-passwd mypassword

# Prompt hidden for the password
bin/filex-passwd

# Write the hash straight into the user's own ~/.config/filex.yaml
bin/filex-passwd -user alice

# Same, but into a specific preferences file (e.g. a user with no passwd entry)
bin/filex-passwd -user alice -prefs /srv/filex/alice.yaml

# Pipe a password from a script
echo -n mypassword | bin/filex-passwd -user alice
```

> **Note**: after changing a password, restart filex so the login handler picks
> up the new hash. The tool reads the preferences file, sets `password_hash` for
> the named user, and rewrites it atomically (comments and formatting preserved),
> keeping the permissions (default `0600`) of the existing file.

## Docker

```bash
# Build and run with docker-compose
docker-compose up -d
```

Files are served from `./data`, config is read (read-only) from
`./config.yaml`, and user preferences are persisted to `./prefs.yaml`, which
docker-compose mounts as `/root/.config/filex.yaml` inside the container
(`filex-passwd -user NAME` inside the container writes the same file). Create
`prefs.yaml` first with `touch prefs.yaml` so Docker mounts it as a file rather
than creating a directory.

## Makefile Targets

| Target           | Description                                      |
|------------------|--------------------------------------------------|
| `all`            | Default target; same as `build`                  |
| `build`          | Build binaries to `bin/filex` and `bin/filex-passwd` |
| `test`           | Run all Go tests                                 |
| `check`          | Pre-commit gate: gofmt, vet, build, tests, JS syntax |
| `lint`           | Run `go vet ./...`                               |
| `install`        | Build and install `filex` and `filex-passwd` to `/usr/local/bin` |
| `install-openrc` | Install the OpenRC init script and create the log directory |
| `clean`          | Remove build artifacts                           |
| `docker`         | Build Docker image `filex:latest`                |
| `sample-config`  | Generate a no-auth sample `config.sample.yaml` (backs up an existing file) |
| `sample-config-auth` | Generate an auth sample `config.sample.yaml` with a sample user (backs up an existing file) |

## API

The HTTP API is documented in the OpenAPI 3.0 specification at
[`api/openapi.yaml`](api/openapi.yaml). All endpoints live under `/api/`
and authenticate via the `filex_session` cookie unless auth is disabled.

## OpenRC (Alpine Linux)

```bash
# Install binary and OpenRC init script
# (creates /var/log/filex if it does not exist)
make install
make install-openrc
rc-update add filex default
rc-service filex start
```

## License

MIT
