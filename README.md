# filex

A fast, self-hosted web-based file browser with a dark theme, built in Go.

## Features

- 📁 Directory listing with icons, size, and modification time
- 📤 File upload (drag-and-drop or click), with progress bar
- 📥 File download
- ✏️ In-browser text editor
- 🖼️ Inline previews for images, video, audio, and text files
- 📂 Create folders, rename, delete (single or bulk), move files
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

### No-auth mode (single user, no login required)

```yaml
host: "0.0.0.0"
port: 8080
base_path: "/data"
show_dotfiles: false
session_ttl_days: 30   # session lifetime in days (default: 30)
favourites:
  - name: "Home"
    path: "/"
```

### Multi-user mode (login required)

When `users:` is defined, a login page is shown and each user gets their own
path jail, favourites, and optional UID/GID for file ownership.

```yaml
host: "0.0.0.0"
port: 8080
show_dotfiles: false
session_ttl_days: 30   # session lifetime in days (default: 30)

users:
  - username: "alice"
    password_hash: "$2a$10$..."   # bcrypt hash — see below
    base_path: "/data/alice"
    uid: 1001                     # omit to skip chown
    gid: 1001
    favourites:
      - name: "Home"
        path: "/"
  - username: "bob"
    password_hash: "$2a$10$..."
    base_path: "/data/bob"
```

### Generating password hashes

```bash
# Using htpasswd (apache2-utils / httpd-tools)
htpasswd -bnBC 10 "" mypassword | tr -d ':\n'

# Using Python
python3 -c "import bcrypt; print(bcrypt.hashpw(b'mypassword', bcrypt.gensalt(10)).decode())"
```

## Docker

```bash
# Build and run with docker-compose
docker-compose up -d
```

Files are served from `./data` and config is read from `./config.yaml`.

## Makefile Targets

| Target           | Description                                      |
|------------------|--------------------------------------------------|
| `build`          | Build binary to `bin/filex`                      |
| `test`           | Run all tests                                    |
| `install`        | Build and install binary to `/usr/local/bin`     |
| `install-openrc` | Install OpenRC init script to `/etc/init.d/filex`|
| `clean`          | Remove build artifacts                           |
| `docker`         | Build Docker image `filex:latest`                |

## API

All endpoints are under `/api/`:

| Method | Endpoint              | Description              |
|--------|-----------------------|--------------------------|
| GET    | `/api/list?path=...`  | List directory           |
| GET    | `/api/download?path=` | Download file            |
| POST   | `/api/upload?path=`   | Upload file (multipart)  |
| POST   | `/api/mkdir`          | Create directory         |
| POST   | `/api/rename`         | Rename file/directory    |
| POST   | `/api/delete`         | Delete file(s)           |
| POST   | `/api/move`           | Move file/directory      |
| GET    | `/api/read?path=...`  | Read text file content   |
| POST   | `/api/write`          | Write text file          |
| GET    | `/api/info?path=...`  | File + disk info         |
| GET    | `/api/favourites`     | List configured favourites |
| GET    | `/api/config`         | Safe server config       |

## OpenRC (Alpine Linux)

```bash
# Install binary and OpenRC init script
make install
make install-openrc
rc-update add filex default
rc-service filex start
```

## License

MIT
