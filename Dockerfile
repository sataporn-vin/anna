FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memory-api ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S memory \
    && adduser -S -G memory memory

COPY --from=build /out/memory-api /usr/local/bin/memory-api

USER memory
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/memory-api"]
