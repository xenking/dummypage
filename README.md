# Dummypage

HTTP site with an optional local-first catalog at `/courses`.

## Course catalog

Build a browser snapshot from one or more schema-compatible exports. Repeated
inputs are merged cumulatively, so a newer partial export does not erase data
that existed only in an older snapshot:

```sh
go run ./cmd/courses-data \
  --input /path/to/export-older.json \
  --input /path/to/export-newer.json \
  --output ./data/catalog.json.gz
gzip -t ./data/catalog.json.gz
```

Use `--input-dir /path/to/exports` to read every JSON snapshot in a directory.
If the export references downloaded `.torrent` attachments,
`--torrent-dir /path/to/attachments` adds their computed magnet links without
embedding or serving the original files.

Create a bcrypt password hash without putting the phrase in shell history:

```sh
read -s phrase
echo
printf %s "$phrase" | go run ./cmd/courses-password > ./data/catalog-password.hash
unset phrase
chmod 600 ./data/catalog-password.hash
```

Run locally:

```sh
APP_SERVER_ADDR=127.0.0.1:3000 \
APP_SERVER_COURSES_CATALOG=./data/catalog.json.gz \
APP_SERVER_COURSES_PASSWORD_HASH_FILE=./data/catalog-password.hash \
go run ./cmd
```

Source exports, normalized snapshots, attachments, and password hashes are
runtime data excluded from Git and the Docker build context. Mount the runtime
directory read-only in production; replacing the snapshot or hash does not
require rebuilding the image.

The server password protects catalog distribution, not data already cached in
the browser. “Забыть базу на этом устройстве” removes the local IndexedDB copy.

## Verification

```sh
go test -count=1 ./...
go vet ./...
govulncheck ./...
docker build --platform linux/amd64 -t dummypage:local .
```
