# Production image for phronesis.
#
# goreleaser (via `dockers:` in .goreleaser.yaml) and `make image` (local,
# Colima) both put the already-built linux binary in the build context, so
# this is a "pack the binary" recipe, not a `go build`.
#
# Satisfies: B1 (Docker channel), T1 (per-arch image), RT-3, RT-11.

# distroless has no shell, so a tiny stage creates /data owned by the
# nonroot user. A fresh named volume mounted at /data inherits it.
FROM busybox:1.37 AS prep
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=prep --chown=65532:65532 /data /data
COPY phronesis /usr/local/bin/phronesis

# Absolute data paths under the /data volume. PHRONESIS_ENV=production
# makes the server refuse the default admin password and stub OIDC.
ENV PHRONESIS_ENV=production \
    PHRONESIS_ADDR=:8080 \
    PHRONESIS_PAGES_DIR=/data/pages \
    PHRONESIS_BLOB_DIR=/data/blobs \
    PHRONESIS_AUDIT_LOG=/data/audit.log \
    PHRONESIS_STORE_PATH=/data/phronesis.db \
    PHRONESIS_OAUTH_KEY_PATH=/data/oauth-key.pem \
    PHRONESIS_SNAPSHOT_DIR=/data/snapshots

USER nonroot:nonroot
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/phronesis"]
