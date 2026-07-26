# Dummypage

Small HTTP site.

## Verification

```sh
go test -count=1 ./...
go vet ./...
govulncheck ./...
docker build --platform linux/amd64 -t dummypage:local .
```
