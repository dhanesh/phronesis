# Minimal production image for phronesis.
#
# goreleaser (via `dockers:` block in .goreleaser.yaml) passes the
# already-built binary into the build context, so this Dockerfile is
# intentionally a "pack the binary into scratch+certs" recipe — not a
# multi-stage `go build`. Keeps the image small and the build fast.
#
# Satisfies: B1 (Docker channel), T1 (per-arch image), RT-3, RT-11.

FROM gcr.io/distroless/static-debian12:nonroot

COPY phronesis /usr/local/bin/phronesis

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/phronesis"]
