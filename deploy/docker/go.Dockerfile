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
RUN addgroup -S relythra && adduser -S -G relythra relythra
COPY --from=build /out/service /usr/local/bin/relythra
USER relythra
ENTRYPOINT ["/usr/local/bin/relythra"]
