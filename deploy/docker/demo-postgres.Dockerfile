# syntax=docker/dockerfile:1
FROM postgres:17-alpine@sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b
COPY --chmod=0755 deploy/demo/postgres/001-demo.sh /docker-entrypoint-initdb.d/001-demo.sh
COPY --chmod=0755 deploy/demo/postgres/entrypoint.sh /usr/local/bin/demo-postgres-entrypoint
ENTRYPOINT ["/usr/local/bin/demo-postgres-entrypoint"]
CMD ["postgres"]
