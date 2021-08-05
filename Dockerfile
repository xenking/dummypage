FROM golang:1.15-alpine

WORKDIR /dummypage

COPY vendor ./vendor
COPY . .

RUN CGO_ENABLED=0 GO111MODULE=on GOOS=linux GOARCH=amd64 go build -mod vendor -ldflags='-w -s' -o dummypage ./cmd
ENTRYPOINT ["./dummypage"]
