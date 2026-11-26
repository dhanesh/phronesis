# Deploying phronesis

Two supported ways to run phronesis with single sign-on: **Docker Compose**
and **Kubernetes with Helm**. Both are tested end to end in Colima (Docker
and k3s) with a real [Dex](https://dexidp.io) OIDC provider by
`make compose-test` and `make helm-test`.

Both use the same container image (`Dockerfile`):

- distroless, runs as `nonroot` (uid 65532), no shell;
- all data under the `/data` volume: pages, SQLite (users, keys, audit),
  blobs, OAuth signing key, snapshots;
- `PHRONESIS_ENV=production`: the server refuses to start with the default
  admin password or the development OIDC stub;
- `phronesis -healthcheck <url>` for health checks (no curl in the image).

Build it locally with `make image` (tag `phronesis:dev`, Docker context
`colima`; override with `IMAGE=` and `DOCKER_CONTEXT=`). Release images are
built by goreleaser from the same Dockerfile.

## Docker Compose (phronesis + Dex)

```bash
make image
export PHRONESIS_ADMIN_PASSWORD='<a long random secret>'
docker compose -f deploy/compose/compose.yaml up -d
open http://localhost:8080
```

- "Sign in with SSO" goes to Dex. Test users: `viewer@example.com /
  viewer-pass`, `editor@example.com / editor-pass`, `admin@example.com /
  admin-pass`. **Change them before you share a beta** (edit the
  `staticPasswords` in `compose.yaml`; hash with
  `htpasswd -bnBC 10 "" '<password>' | tr -d ':\n'`, and write each `$` as
  `$$`).
- Every SSO user starts as **viewer**. Sign in with the password admin
  (`admin` / `$PHRONESIS_ADMIN_PASSWORD`), open the command palette
  (Cmd/Ctrl-K) and choose "Manage users" to promote people.
- Ports: `PHRONESIS_PORT` (default 8080) and `DEX_PORT` (default 5556).
  The Dex issuer and redirect URI follow these values.
- `DEX_CLIENT_SECRET` overrides the client secret shared by Dex and
  phronesis.
- Use another image with `PHRONESIS_IMAGE=ghcr.io/dhanesh/phronesis:<tag>`.

### Backup and restore (Compose)

```bash
deploy/compose/backup.sh          # writes ./phronesis-backup-<time>.tgz
```

The script stops phronesis for a consistent SQLite copy, archives the
`phronesis-data` volume, and starts phronesis again. `OUT=<dir>` picks the
directory (Colima shares only your home directory with the VM). Restore
instructions are at the top of the script.

## Kubernetes (Helm)

```bash
helm install phronesis deploy/helm/phronesis \
  --set publicURL=https://wiki.example.com \
  --set auth.adminPassword='<a long random secret>' \
  --set oidc.enabled=true \
  --set oidc.issuer=https://dex.example.com/dex \
  --set oidc.clientSecret='<client secret registered in the IdP>' \
  --set ingress.enabled=true --set ingress.host=wiki.example.com
```

What the chart does:

- one replica with the `Recreate` strategy: SQLite and on-disk pages need a
  single writer;
- a PVC for `/data` (`persistence.size`, default 5Gi), kept on uninstall;
- `fsGroup: 65532`, read-only root filesystem, all capabilities dropped;
- readiness `/api/readyz`, liveness `/api/health`;
- refuses to render without `publicURL` and an admin password, and refuses
  the built-in default password. Use `auth.existingSecret` (keys
  `admin-password`, `oidc-client-secret`) to keep secrets out of values.
- `oidc.providerURL` lets phronesis reach the IdP in-cluster (for example
  `http://dex.auth:5556/dex`) while browsers use `oidc.issuer`.

Register the redirect URI `<publicURL>/api/auth/oidc/callback` with your
IdP.

### Backup (Helm)

Scale to zero, copy the PVC, scale back:

```bash
kubectl scale deploy/phronesis --replicas=0
kubectl run backup --rm -i --restart=Never --image=busybox \
  --overrides='{"spec":{"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"phronesis-data"}}],"containers":[{"name":"backup","image":"busybox","command":["tar","czf","-","-C","/data","."],"volumeMounts":[{"name":"d","mountPath":"/data"}]}]}}' \
  > phronesis-backup.tgz
kubectl scale deploy/phronesis --replicas=1
```

## Using another OIDC provider

Any OpenID Connect provider with discovery works (Keycloak, Okta, Auth0,
Entra ID, Google, Dex). Create a confidential client with the
authorization-code flow and the redirect URI above, then set:

| Variable | Meaning |
|---|---|
| `PHRONESIS_OIDC_ENABLED=1` | turn OIDC on |
| `PHRONESIS_OIDC_ISSUER` | the issuer URL exactly as the IdP's discovery document states it |
| `PHRONESIS_OIDC_CLIENT_ID` / `PHRONESIS_OIDC_CLIENT_SECRET` | the client |
| `PHRONESIS_PUBLIC_URL` | browser-facing URL of phronesis (builds the redirect URI) |
| `PHRONESIS_OIDC_REDIRECT_URL` | optional explicit redirect URI |
| `PHRONESIS_OIDC_PROVIDER_URL` | optional server-to-IdP base URL when it differs from the issuer (container DNS); its discovery document must still name the configured issuer |
| `PHRONESIS_OIDC_AUDIENCE` | optional; defaults to the client id |

The flow is authorization code + PKCE with `state` (bound to the browser
by a cookie) and `nonce`. ID tokens must be RS256-signed; the keys come from
the provider's JWKS. A clock skew of up to 2 minutes is accepted.

Users are matched by the `sub` claim and shown by `name` (or `email`). IdP
groups are not mapped to roles yet: promote users in the app.

## Operating notes

- **Sessions are in memory.** After a restart users sign in again (SSO
  makes this one click). Pages, users, roles, API keys and OAuth clients'
  signing key are on the volume and survive.
- **Single instance.** Do not run two replicas against one volume.
- **TLS** terminates at your ingress or reverse proxy. phronesis marks
  cookies `Secure` when the request arrived over HTTPS
  (`X-Forwarded-Proto: https`).
- **Upgrades** run database migrations at start-up; take a backup first.
  A failed migration stops the server instead of starting half-migrated.
