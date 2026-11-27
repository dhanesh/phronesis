# Production image for phronesis.
#
# goreleaser (via `dockers:` in .goreleaser.yaml) and `make image` (local,
# Colima) both put the already-built linux binary in the build context, so
# this is a "pack the binary" recipe, not a `go build`.
#
# Satisfies: B1 (Docker channel), T1 (per-arch image), RT-3, RT-11.

FROM gcr.io/distroless/static-debian12:nonroot

# /data must be owned by nonroot so a fresh named volume mounted there
# inherits that owner. distroless has no shell, so copy its own empty
# /home/nonroot directory: no RUN step, so cross-arch buildx needs no
# emulation.
COPY --from=gcr.io/distroless/static-debian12:nonroot --chown=65532:65532 /home/nonroot /data
COPY phronesis /usr/local/bin/phronesis

# Absolute data paths under the /data volume. PHRONESIS_ENV=production
# makes the server refuse the default admin password and stub OIDC.
ENV PHRONESIS_ENV=production \
    PHRONESIS_ADDR=:8080 \
    PHRONESIS_PAGES_DIR=/data/pages \
    PHRONESIS_BLOB_DIR=/data/blobs \
    PHRONESIS_AUDIT_LOG=/data/audit.log \
    PHRONESIS_STORE_PATH=/data/phronesis.db \
    PHRONESIS_OAUTH_KEY_PATH=/data/oauth-key.pem
# Snapshots (PHRONESIS_SNAPSHOT_DIR) are opt-in: they are hourly full copies
# with no pruning yet, so they would grow the volume without bound.

USER nonroot:nonroot
VOLUME ["/data"]
EXPOSE 8080
# No shell or curl in distroless: the binary probes itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/phronesis", "-healthcheck", "http://127.0.0.1:8080/api/health"]
ENTRYPOINT ["/usr/local/bin/phronesis"]
