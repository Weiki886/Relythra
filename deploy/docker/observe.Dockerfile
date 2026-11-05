# syntax=docker/dockerfile:1
ARG IMAGE
FROM golang:1.26-alpine AS probe
WORKDIR /src
COPY cmd/relythra-probe/main.go ./main.go
RUN CGO_ENABLED=0 go build -trimpath -o /probe main.go
FROM ${IMAGE}
COPY --from=probe /probe /usr/local/bin/relythra-probe
