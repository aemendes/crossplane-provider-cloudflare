# crossplane-provider-cloudflare

Small, native Go [Crossplane](https://crossplane.io) providers for Cloudflare Zero Trust, built directly on the
official [`cloudflare-go`](https://github.com/cloudflare/cloudflare-go) SDK.

Instead of one provider that covers all of Cloudflare, this repository ships **one provider per area**, each with
its own Go module, Crossplane package and API group. Install only what you use; two packages never compete for
the same CRD.

| Provider | Package | API group | Kinds |
|---|---|---|---|
| [`tunnel/`](tunnel/) | `ghcr.io/aemendes/provider-cloudflare-tunnel-pkg` | `tunnel.cloudflare.crossplane.io/v1alpha1` | `Tunnel`, `TunnelConfig`, `DNSRecord` |
| [`access/`](access/) | `ghcr.io/aemendes/provider-cloudflare-access-pkg` | `access.cloudflare.crossplane.io/v1alpha1` | `AccessApplication`, `Policy` |

With both installed you can declare, in git, everything needed to publish a service in a Kubernetes cluster to the
internet through a [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
and put [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/policies/access/) in front of it:
the tunnel, its connector token, the hostname routing, the DNS record and the login policy.

> **Status:** `v1alpha1`. The APIs are small and stable in practice, but may still change. Both providers run in
> production for the author.

---

## Contents

- [Requirements](#requirements)
- [Installation](#installation)
- [Configuration (ProviderConfig)](#configuration-providerconfig)
- [provider-cloudflare-tunnel](#provider-cloudflare-tunnel)
  - [Tunnel](#tunnel)
  - [TunnelConfig](#tunnelconfig)
  - [DNSRecord](#dnsrecord)
- [provider-cloudflare-access](#provider-cloudflare-access)
  - [Policy](#policy)
  - [AccessApplication](#accessapplication)
- [End-to-end example: expose a service through a tunnel, behind Access](#end-to-end-example)
- [Behaviour worth knowing](#behaviour-worth-knowing)
- [Development](#development)
- [Release process](#release-process)
- [Security](#security)
- [License](#license)

---

## Requirements

- A Kubernetes cluster with **Crossplane v1.17+ or v2.x** installed
  ([install guide](https://docs.crossplane.io/latest/software/install/)).
- A Cloudflare account and a **Cloudflare API token** (not the legacy Global API Key) with only the permissions
  of the providers you install:

  | Provider | Token permissions |
  |---|---|
  | tunnel | Account → **Cloudflare Tunnel: Edit**, Zone → **DNS: Edit** (for the zones you route hostnames in) |
  | access | Account → **Access: Apps and Policies: Edit** |

  Create it under *My Profile → API Tokens → Create Token → Custom token*.
- Your Cloudflare **account ID** (dashboard → any domain → *Overview*, right sidebar) and, for `DNSRecord`, the
  **zone ID** of each domain.

## Installation

The packages are public on GitHub Container Registry; no pull secret is needed. Pick a version from the
[packages page](https://github.com/aemendes?tab=packages&repo_name=crossplane-provider-cloudflare) (tags are
`v0.1.<build>`); pinning by digest (`...-pkg@sha256:...`) is recommended for production.

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-cloudflare-tunnel
spec:
  package: ghcr.io/aemendes/provider-cloudflare-tunnel-pkg:v0.1.X   # or @sha256:<digest>
---
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-cloudflare-access
spec:
  package: ghcr.io/aemendes/provider-cloudflare-access-pkg:v0.1.X   # or @sha256:<digest>
```

```shell
kubectl apply -f providers.yaml
kubectl get providers.pkg.crossplane.io   # wait for INSTALLED=True, HEALTHY=True
```

Optional: the providers poll Cloudflare every minute by default. To poll less often, attach a
`DeploymentRuntimeConfig`:

```yaml
apiVersion: pkg.crossplane.io/v1beta1
kind: DeploymentRuntimeConfig
metadata:
  name: provider-cloudflare-tunnel
spec:
  deploymentTemplate:
    spec:
      selector: {}
      template:
        spec:
          containers:
            - name: package-runtime
              args: ["--poll=10m"]
```

and reference it from the `Provider` with `spec.runtimeConfigRef.name: provider-cloudflare-tunnel`.

Flags accepted by both providers:

| Flag | Default | Description |
|---|---|---|
| `--poll` | `1m` | How often each resource is checked against Cloudflare for drift. |
| `--sync` | `1h` | Controller manager resync period. |
| `--max-reconcile-rate` | `10` | Maximum reconciles per second across all resources. |
| `--leader-election` | `false` | Enable leader election (when running more than one replica). |
| `--health-addr` | `:8081` | Address of the `/healthz` endpoint. |
| `--debug` | `false` | Debug logging. |

## Configuration (ProviderConfig)

Store the API token in a Secret (use [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets),
[External Secrets](https://external-secrets.io) or similar to keep it out of git in plain text):

```shell
kubectl -n crossplane-system create secret generic cloudflare-api-creds \
  --from-literal=credentials='<your-api-token>'
```

Each provider has its own `ProviderConfig` kind (Crossplane packages cannot share a CRD), but both can point at
the same Secret. Managed resources use the `ProviderConfig` named `default` unless they set
`spec.providerConfigRef.name`.

```yaml
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      namespace: crossplane-system
      name: cloudflare-api-creds
      key: credentials
---
apiVersion: access.cloudflare.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      namespace: crossplane-system
      name: cloudflare-api-creds
      key: credentials
```

All resources below are cluster-scoped managed resources and support the standard Crossplane fields
(`providerConfigRef`, `deletionPolicy`, `managementPolicies`, `writeConnectionSecretToRef`).

---

## provider-cloudflare-tunnel

Manages [remotely-managed Cloudflare Tunnels](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/get-started/create-remote-tunnel/):
the tunnel itself, its public hostname routing, and the DNS records pointing at it. It does not run the connector:
deploy `cloudflared` yourself (see the [end-to-end example](#end-to-end-example)).

### Tunnel

A Cloudflare Tunnel. Publishes the connector token, so `cloudflared` can run without any hand-made secret.

| Field (`spec.forProvider`) | Type | Required | Description |
|---|---|---|---|
| `accountId` | string | yes | Cloudflare account ID. Immutable. |
| `name` | string | no | Tunnel name shown in the dashboard. Defaults to the resource's `metadata.name`. Must be unique in the account. |

| Status (`status.atProvider`) | Description |
|---|---|
| `id` | Tunnel ID (UUID). |
| `status` | Connectivity reported by Cloudflare: `inactive`, `degraded`, `healthy` or `down`. |
| `createdAt` | Creation time. |

**Connection secret** (`spec.writeConnectionSecretToRef`): `tunnelToken` (the token `cloudflared` needs) and
`tunnelId`. The token is re-fetched on every reconcile, so a deleted secret is recreated automatically.

```yaml
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: Tunnel
metadata:
  name: my-cluster
spec:
  forProvider:
    accountId: <account-id>
    name: my-cluster
  writeConnectionSecretToRef:
    namespace: cloudflared
    name: cloudflared-tunnel
```

### TunnelConfig

The ingress rules of one tunnel: which public hostname (and path) goes to which origin service. Cloudflare
evaluates rules top to bottom; the **last rule must be a catch-all** without `hostname`.

| Field (`spec.forProvider`) | Type | Required | Description |
|---|---|---|---|
| `accountId` | string | yes | Must match the tunnel's account. |
| `tunnelId` | string | one of | Tunnel ID. Immutable. |
| `tunnelIdRef` / `tunnelIdSelector` | reference / selector | one of | Resolve `tunnelId` from a `Tunnel` resource. |
| `ingress` | list of rules | yes (≥ 1) | Ordered routing rules, see below. |

| Ingress rule field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | all but the last | Public hostname, e.g. `app.example.com`. |
| `service` | string | yes | Origin, e.g. `http://my-svc.my-ns.svc.cluster.local:8080`, or `http_status:404` for the catch-all. |
| `path` | string | no | Restrict the rule to requests under this path. |
| `noTLSVerify` | bool | no | Skip TLS verification for HTTPS origins with self-signed certificates. |

| Status | Description |
|---|---|
| `status.atProvider.version` | Configuration revision reported by Cloudflare. |

```yaml
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: TunnelConfig
metadata:
  name: my-cluster
spec:
  forProvider:
    accountId: <account-id>
    tunnelIdRef:
      name: my-cluster
    ingress:
      - hostname: app.example.com
        service: http://app.default.svc.cluster.local:8080
      - service: http_status:404
```

### DNSRecord

A proxied CNAME that points a hostname at a tunnel (`<tunnel-id>.cfargotunnel.com`). Only tunnel CNAMEs are
modelled; it is not a general DNS record resource.

| Field (`spec.forProvider`) | Type | Required | Description |
|---|---|---|---|
| `zoneId` | string | yes | Zone the record lives in. Immutable. |
| `name` | string | yes | Hostname, e.g. `app.example.com`. |
| `tunnelId` | string | one of | Tunnel to route to. |
| `tunnelIdRef` / `tunnelIdSelector` | reference / selector | one of | Resolve `tunnelId` from a `Tunnel`. |
| `proxied` | bool | no (default `true`) | Proxy through Cloudflare. Tunnel hostnames need it. |

| Status (`status.atProvider`) | Description |
|---|---|
| `id` | DNS record ID. |
| `content` | CNAME target reported by Cloudflare. |

```yaml
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: DNSRecord
metadata:
  name: app
spec:
  forProvider:
    zoneId: <zone-id>
    name: app.example.com
    tunnelIdRef:
      name: my-cluster
    proxied: true
```

---

## provider-cloudflare-access

Manages [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/policies/access/) reusable policies and
the applications they protect. Deliberately scoped: `self_hosted` and `saas` (OIDC) applications, and the rule
types listed below.

### Policy

A reusable, account-level Access policy. Attach it to one or more `AccessApplication`s.

| Field (`spec.forProvider`) | Type | Required | Description |
|---|---|---|---|
| `accountId` | string | yes | Cloudflare account ID. Immutable. |
| `name` | string | yes | Name shown in the dashboard. |
| `decision` | enum | yes | `allow`, `deny`, `bypass` (skip Access, e.g. for webhooks) or `nonIdentity` (service tokens without a login). |
| `include` | list of rules | yes (≥ 1) | OR: a request must match at least one. |
| `exclude` | list of rules | no | NOT: a request must match none. |
| `require` | list of rules | no | AND: a request must match all. |
| `sessionDuration` | string | no | e.g. `24h` (Cloudflare's default). |

Each **rule** sets exactly one of:

| Rule field | Type | Matches |
|---|---|---|
| `everyone` | `true` | everyone |
| `email` | string | one exact email address |
| `emailDomain` | string | any address in a domain, e.g. `example.com` |
| `ipRange` | string | an IPv4/IPv6 CIDR |
| `anyValidServiceToken` | `true` | any valid Access service token of the account |

| Status | Description |
|---|---|
| `status.atProvider.id` | Policy ID (UUID). |

```yaml
apiVersion: access.cloudflare.crossplane.io/v1alpha1
kind: Policy
metadata:
  name: admins
spec:
  forProvider:
    accountId: <account-id>
    name: Admins
    decision: allow
    include:
      - email: alice@example.com
      - emailDomain: example.com
    sessionDuration: 24h
```

### AccessApplication

An Access application. `self_hosted` gates one or more hostnames (typically ones routed through a tunnel);
`saas` makes Cloudflare an OIDC identity provider for another application.

| Field (`spec.forProvider`) | Type | Required | Description |
|---|---|---|---|
| `accountId` | string | yes | Cloudflare account ID. Immutable. |
| `type` | enum | no (default `self_hosted`) | `self_hosted` or `saas`. Immutable. |
| `name` | string | no | Name shown in the dashboard. Defaults to `metadata.name`. |
| `domain` | string | self_hosted | Hostname (optionally with path) to protect, e.g. `app.example.com`. |
| `additionalDomains` | list of strings | no | More hostnames under the same application and policies. self_hosted only. |
| `sessionDuration` | string | no | e.g. `24h`. self_hosted only. |
| `httpOnlyCookieAttribute` | bool | no | HttpOnly flag on the session cookie (Cloudflare default `true`). self_hosted only. |
| `policyIds` | list of strings | one of | Policy IDs, in evaluation order (first match wins). |
| `policyIdRefs` / `policyIdSelector` | references / selector | one of | Resolve `policyIds` from `Policy` resources, in order. |
| `oidc` | object | saas | OIDC settings, see below. |

| `oidc` field | Type | Required | Description |
|---|---|---|---|
| `redirectUris` | list of strings | yes (≥ 1) | Where Cloudflare may send codes and tokens. |
| `grantTypes` | list of strings | no | e.g. `authorization_code`, `refresh_tokens`. |
| `scopes` | list of strings | no | e.g. `openid`, `email`, `profile`, `groups`. |
| `accessTokenLifetime` | string | no | e.g. `5m`. |
| `refreshTokenLifetime` | string | no | e.g. `90d`; omit to disable refresh tokens. |
| `appLauncherVisible` | bool | no | Show the app in the App Launcher. |

| Status (`status.atProvider`) | Description |
|---|---|
| `id` | Application ID (UUID). |
| `aud` | Audience tag; origins use it to validate Access JWTs. |
| `clientId` | OIDC client ID (saas only). |

**Connection secret** (saas only): `clientId` and `clientSecret`. Cloudflare returns the client secret **only once,
on creation**, so set `writeConnectionSecretToRef` before the first apply.

```yaml
apiVersion: access.cloudflare.crossplane.io/v1alpha1
kind: AccessApplication
metadata:
  name: app
spec:
  forProvider:
    accountId: <account-id>
    type: self_hosted
    name: App
    domain: app.example.com
    sessionDuration: 24h
    policyIdRefs:
      - name: admins
```

---

## End-to-end example

Publish `app.default.svc.cluster.local:8080` at `https://app.example.com`, reachable only by `@example.com`
users. Together with the `ProviderConfig`s above:

```yaml
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: Tunnel
metadata:
  name: my-cluster
spec:
  forProvider:
    accountId: <account-id>
  writeConnectionSecretToRef:
    namespace: cloudflared
    name: cloudflared-tunnel
---
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: TunnelConfig
metadata:
  name: my-cluster
spec:
  forProvider:
    accountId: <account-id>
    tunnelIdRef: {name: my-cluster}
    ingress:
      - hostname: app.example.com
        service: http://app.default.svc.cluster.local:8080
      - service: http_status:404
---
apiVersion: tunnel.cloudflare.crossplane.io/v1alpha1
kind: DNSRecord
metadata:
  name: app
spec:
  forProvider:
    zoneId: <zone-id>
    name: app.example.com
    tunnelIdRef: {name: my-cluster}
---
apiVersion: access.cloudflare.crossplane.io/v1alpha1
kind: Policy
metadata:
  name: example-staff
spec:
  forProvider:
    accountId: <account-id>
    name: Example staff
    decision: allow
    include:
      - emailDomain: example.com
---
apiVersion: access.cloudflare.crossplane.io/v1alpha1
kind: AccessApplication
metadata:
  name: app
spec:
  forProvider:
    accountId: <account-id>
    domain: app.example.com
    policyIdRefs: [{name: example-staff}]
---
# The connector. It reads the token the Tunnel above writes.
apiVersion: v1
kind: Namespace
metadata:
  name: cloudflared
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cloudflared
  namespace: cloudflared
spec:
  replicas: 2
  selector:
    matchLabels: {app: cloudflared}
  template:
    metadata:
      labels: {app: cloudflared}
    spec:
      containers:
        - name: cloudflared
          image: cloudflare/cloudflared:2026.10.2
          args: [tunnel, --no-autoupdate, --metrics, "0.0.0.0:2000", run]
          env:
            - name: TUNNEL_TOKEN
              valueFrom:
                secretKeyRef: {name: cloudflared-tunnel, key: tunnelToken}
          readinessProbe:
            httpGet: {path: /ready, port: 2000}
```

`kubectl get managed` shows every resource; each should become `READY=True` and `SYNCED=True`.

## Behaviour worth knowing

- **Adoption by name.** If a `Tunnel`, `DNSRecord`, `AccessApplication` or `Policy` with the same name already exists in
  Cloudflare, the provider adopts it instead of failing on a duplicate. This makes it safe to bring existing
  resources under management: start with `managementPolicies: ["Observe", "LateInitialize"]`, check
  `kubectl describe` reports no drift, then remove `managementPolicies` to hand over full control.
- **Drift is corrected.** Changes made in the dashboard are overwritten on the next poll. A `Policy` rule type this
  provider does not model (SAML/Okta groups, device posture, geo, ...) counts as drift and is removed.
- **Deletion.** With the default `deletionPolicy: Delete`, deleting the Kubernetes resource deletes it in Cloudflare.
  Use `deletionPolicy: Orphan` to keep it.
- **Kind name.** The Access application kind is `AccessApplication`, not `Application`, so it never collides with
  Argo CD's `applications.argoproj.io` in `kubectl get applications`.
- **Not modelled:** SAML SaaS and RDP/SSH/VNC/App Launcher application types; identity-provider group, device
  posture, geo and certificate rules; non-tunnel DNS records; tunnel `originRequest` options other than
  `noTLSVerify`. Contributions welcome.

## Development

Each provider is an independent Go module. Inside `tunnel/` or `access/`:

```shell
make generate   # regenerate deepcopy code and package/crds/*.yaml from the API types
make build
make test
```

Layout of a provider:

| Path | Contents |
|---|---|
| `apis/v1alpha1` | `ProviderConfig` / `ProviderConfigUsage` |
| `apis/<kind>/v1alpha1` | managed resource types and cross-resource referencers |
| `internal/clients/cloudflare` | thin wrapper over `cloudflare-go`, only the services this provider uses |
| `internal/controller/<kind>` | `Observe` / `Create` / `Update` / `Delete` |
| `cmd/provider` | manager entrypoint |
| `package/` | Crossplane package metadata and generated CRDs |
| `examples/` | one manifest per kind |

Run a provider against a cluster from your machine (CRDs from `package/crds` must be installed):

```shell
kubectl apply -f package/crds
go run ./cmd/provider --debug
```

**Adding a provider:** `scripts/new-provider.sh <name> "<description>"` scaffolds `<name>/` with the generic
ProviderConfig plumbing, entrypoint, Makefile, Dockerfile and package metadata. Write the types, client methods and
controllers by hand, using `tunnel/` as the reference, then add `<name>` to the filters and `workflow_dispatch`
options in `.github/workflows/build.yaml`. Keep each provider at its own API group
(`<name>.cloudflare.crossplane.io`) and pin the same dependency versions across providers.

## Release process

[`.github/workflows/build.yaml`](.github/workflows/build.yaml) runs on every push to `main` and builds only the
provider(s) whose directory changed (the manual trigger can build either or both). For each it runs
`go build`, `go vet` and `go test`, then pushes the runtime image and the Crossplane package to GHCR, tagged
`v0.1.<run number>`, and prints the package digest.

Optionally, the workflow sends a `pin-package` [repository dispatch](https://docs.github.com/en/rest/repos/repos#create-a-repository-dispatch-event)
(`client_payload: {image, ref}`) to GitOps repositories so they can pin the new digest automatically. Configure:

- repository variable `PIN_PACKAGE_REPOS`: space-separated `owner/repo` list;
- repository secret `PIN_DISPATCH_TOKEN`: a fine-grained token with **Contents: Read and write** on exactly those
  repositories.

Without them the step is skipped.

## Security

See [SECURITY.md](SECURITY.md) for how to report a vulnerability.

## License

[Apache License 2.0](LICENSE).
