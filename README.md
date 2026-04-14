# # filex

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
- 🔒 Path jail — cannot escape the configured base directory
- 🌑 Dark, high-contrast UI with mobile-responsive layout
- 📦 Single binary with embedded assets (no external dependencies at runtime)

## Quick Start

```bash
# Build
make build

# Run (serves current directory on :8080)
./bin/filex

# Run with a config file
./bin/filex -config config.yaml
```

## Configuration (`config.yaml`)

```yaml
host: "0.0.0.0"
port: 8080
base_path: "/data"
show_dotfiles: false
favourites:
  - name: "Home"
    path: "/"
  - name: "Documents"
    path: "/documents"
```

## Docker

```bash
# Build and run with docker-compose
docker-compose up -d
```

Files are served from `./data` and config is read from `./config.yaml`.

## Makefile Targets

| Target    | Description                            |
|-----------|----------------------------------------|
| `build`   | Build binary to `bin/filex`            |
| `test`    | Run all tests                          |
| `install` | Install binary to `$GOPATH/bin`        |
| `clean`   | Remove build artifacts                 |
| `docker`  | Build Docker image `filex:latest`      |

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
cp init/filex.openrc /etc/init.d/filex
chmod +x /etc/init.d/filex
rc-update add filex default
rc-service filex start
```

## License

MIT
