# Multi-stage build for the P0 API.
# Build context is services/api (see ../deploy/docker-compose.yml).

FROM golang:1.23-alpine AS build
WORKDIR /src

# Use a China-reachable proxy by default so builds work behind common firewalls.
ENV GOPROXY=https://goproxy.cn,direct GOSUMDB=off CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/api ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/api /app/api
EXPOSE 8080
ENV PORT=8080
ENTRYPOINT ["/app/api"]
