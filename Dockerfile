FROM golang:1.24 AS build
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .

RUN CGO_ENABLED=0 GO111MODULE=on GOOS=linux GOARCH=amd64 go build -v -ldflags='-w -s' -o /app/build/service /app/cmd

FROM alpine
WORKDIR /app
COPY --from=build /app/build/service /app/service
COPY --from=build /app/static /app/static
ENTRYPOINT ["/app/service"]
