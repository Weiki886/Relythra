# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
ARG BINARY=relythra-api
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/service "./cmd/${BINARY}"

FROM alpine:3.22
RUN addgroup -S -g 10001 relythra \
    && adduser -S -D -H -u 10001 -G relythra relythra \
    && apk add --no-cache su-exec
COPY --from=build /out/service /usr/local/bin/relythra
COPY --chmod=0755 deploy/docker/entrypoint.sh /usr/local/bin/relythra-entrypoint
ENTRYPOINT ["/usr/local/bin/relythra-entrypoint"]
