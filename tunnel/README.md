# provider-cloudflare-tunnel

A native Go Crossplane provider for [Cloudflare Zero Trust
Tunnels](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/),
managing `Tunnel`, `TunnelConfig`, and `DNSRecord` resources
(`tunnel.cloudflare.crossplane.io/v1alpha1`) directly against the Cloudflare REST
API via the official [`cloudflare-go/v7`](https://github.com/cloudflare/cloudflare-go)
SDK. Deliberately scoped to tunnels only — not a general-purpose Cloudflare
provider; no Access, WAF, firewall rules, or broader zone/account management.

## Layout

- `apis/v1alpha1` — `ProviderConfig` / `ProviderConfigUsage` (credential wiring, standard Crossplane pattern)
- `apis/tunnel/v1alpha1` — the `Tunnel` managed resource (account-scoped)
- `apis/tunnelconfig/v1alpha1` — the `TunnelConfig` managed resource (a tunnel's ingress routing rules; 1:1 with its `Tunnel`)
- `apis/dnsrecord/v1alpha1` — the `DNSRecord` managed resource (the CNAME that routes a hostname to a tunnel; zone-scoped, 1:many per `Tunnel`)
- `internal/clients/cloudflare` — thin wrapper around `cloudflare-go/v7`, scoped to the Tunnel/Configurations/Token/DNS Records services
- `internal/controller/tunnel` — `Observe`/`Create`/`Update`/`Delete` against Cloudflare's `cfd_tunnel` API
- `internal/controller/tunnelconfig` — same, for ingress config (a full-replace PUT, no partial patch); `Delete` resets to just the mandatory catch-all rule since Cloudflare has no endpoint to remove a tunnel's config outright
- `internal/controller/dnsrecord` — same, for the CNAME record; content is always derived as `<tunnelId>.cfargotunnel.com`, never user-supplied
- `internal/controller/providerconfig` — standard ProviderConfig usage-tracking controller
- `cmd/provider` — manager entrypoint
- `package/` — Crossplane package metadata (`crossplane.yaml`) + generated CRDs

`TunnelConfig`/`DNSRecord` support cross-resource references (`tunnelIdRef`/
`Selector`) so you don't have to copy-paste a `Tunnel`'s resolved Cloudflare ID by
hand — see `apis/tunnel/v1alpha1/referencers.go` for the extractor.

Cloudflare rejects duplicate tunnel names and a hostname can only have one CNAME,
so both `Tunnel` and `DNSRecord` defensively adopt an existing resource by exact
name if the external-name annotation is ever lost before being persisted (e.g. the
pod dying right after a successful Create) — see `Observe`'s adopt-by-name branch
in each controller, and `managed.WithInitializers()` in each `Setup` (disables the
reconciler's default behavior of stamping external-name from the object's own
Kubernetes name before that branch would ever get a chance to run).

Unlike a locally-managed tunnel's one-shot secret, a remotely-managed tunnel's
connector token is re-fetchable at any time via Cloudflare's API — so `Tunnel`'s
`Observe` re-fetches and republishes it on every reconcile, letting the connection
secret self-heal if it's ever lost or deleted out-of-band, rather than only ever
setting it once on `Create`.

`TunnelConfig.ingress[].noTLSVerify` disables TLS certificate verification for
HTTPS origins that present a self-signed or internally-issued certificate — the
one origin-request option modeled out of Cloudflare's ~15, added because a real
ingress rule in use needed it. Everything else under `originRequest` (timeouts,
`http2Origin`, `caPool`, etc.) isn't exposed; extend `IngressRule` the same way
if a future rule needs one of them.

## Build & test locally

```sh
make generate   # regenerates zz_generated.deepcopy.go and package/crds/*.yaml
make build
make test
```

Releases are built by the repository's [build workflow](../.github/workflows/build.yaml); see the
[top-level README](../README.md#release-process).
