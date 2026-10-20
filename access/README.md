# provider-cloudflare-access

A native Go Crossplane provider for Cloudflare Zero Trust Access, managing
`AccessApplication` and `Policy` resources (`access.cloudflare.crossplane.io/v1alpha1`)
directly against the Cloudflare REST API via the official
[`cloudflare-go/v7`](https://github.com/cloudflare/cloudflare-go) SDK. Deliberately
scoped to what's actually in use rather than the full Cloudflare Access surface:
`self_hosted` and `saas` (OIDC identity provider) application types, and a
handful of policy rule types/decisions — not a general-purpose Cloudflare
provider; no SAML SaaS, RDP/VNC/SSH/App-Launcher/etc. application types, no
SAML/Azure/GitHub/Okta/GSuite group rules, device posture, geo, or certificate
rules.

Scaffolded by `scripts/new-provider.sh`; see [`../tunnel/`](../tunnel/) for the sibling provider this
pattern is modeled on.

## Layout

- `apis/v1alpha1` — `ProviderConfig` / `ProviderConfigUsage` (credential wiring, standard Crossplane pattern)
- `apis/policy/v1alpha1` — the `Policy` managed resource (a reusable, account-level Access policy)
- `apis/accessapplication/v1alpha1` — the `AccessApplication` managed resource (a self-hosted Access application; references `Policy` resources by ID, in evaluation order)
- `internal/clients/cloudflare` — thin wrapper around `cloudflare-go/v7`, scoped to the Access Applications and Policies services
- `internal/controller/policy` — `Observe`/`Create`/`Update`/`Delete` against Cloudflare's `/access/policies` API
- `internal/controller/accessapplication` — same, for applications; resolves attached `policyIds` via a to-many cross-resource reference (`reference.ResolveMultiple`)
- `internal/controller/providerconfig` — standard ProviderConfig usage-tracking controller
- `cmd/provider` — manager entrypoint
- `package/` — Crossplane package metadata (`crossplane.yaml`) + generated CRDs

## Scope decisions worth knowing

- **Rule types**: `Policy.include`/`exclude`/`require` support `everyone`, `email`,
  `emailDomain`, `ipRange`, and `anyValidServiceToken`. A rule Cloudflare returns
  that this provider doesn't recognize (SAML/Azure/GitHub/Okta/GSuite group,
  device posture, geo, certificate, etc.) decodes to a zero-value rule, which
  never matches a declared rule — so it correctly shows up as configuration
  drift and gets overwritten by the next `Update`, rather than silently ignored.
- **Decisions**: `allow`, `deny`, `bypass` (skip Access entirely, e.g. for
  webhook endpoints), and `nonIdentity` (evaluate non-identity rules like
  service tokens without a login) — Cloudflare's own `non_identity` value is
  translated to/from camelCase at the client boundary (`toCloudflareDecision`/
  `fromCloudflareDecision`) to stay consistent with the rest of this API.
- **Application types**: `self_hosted` (gates a hostname) and `saas` (Cloudflare
  as an OIDC identity provider for a third-party app, configured via
  `spec.forProvider.oidc`) — SAML SaaS and RDP/VNC/SSH/App-Launcher/etc.
  application types aren't modeled. A `saas` application's OIDC
  `clientId`/`clientSecret` are published via `writeConnectionSecretToRef` on
  `Create` only — Cloudflare returns the secret
  exactly once and never again on subsequent reads.
- **Cloudflare SDK "smart union" decoding**: `AccessApplication.Policies`,
  `AccessApplication.SaaSApp`, and `AccessRule`'s `Email`/`EmailDomain`/`IP`/
  `Everyone`/`AnyValidServiceToken` fields are typed `interface{}` in the SDK,
  but it resolves them to differently-named concrete structs per response type
  (Create vs Get vs Update vs List each get their own generated type for
  conceptually the same shape) rather than a plain map. `decodePolicyIDs`,
  `decodeSaaSApp`, and `decodeRuleValue` in the client all round-trip through
  `encoding/json` instead of type-asserting a specific generated type name —
  this was caught by a failing client test during development, not by
  inspection, and is worth remembering if the SDK ever adds a truly generic
  decode path.
- **Update-loop safety**: `AccessApplication.sessionDuration` (self-hosted) and
  `oidc.accessTokenLifetime`/`refreshTokenLifetime` (saas) all late-initialize
  from Cloudflare's own defaults (assigned server-side if left unset) — otherwise
  comparing an unset `""` against Cloudflare's non-empty default would fire a
  no-op Update every poll forever. `policyIds`/`redirectUris` comparisons treat
  `nil` and an empty slice as equal for the same reason, and are order-sensitive
  (precedence matters); `grantTypes`/`scopes` are compared as unordered sets
  instead, since Cloudflare gives no ordering guarantee for those.
- **No server-side name filter for Policies**: unlike Applications (which support
  `?name=&exact=true`), Cloudflare's List Access Policies endpoint has no name
  filter at all — `ListPoliciesByExactName` pages through results client-side,
  capped at 20 pages of 200 (4000 policies) as a safety backstop.
- **Kind is `AccessApplication`, not `Application`**: Cloudflare itself just
  calls this an "Application", but `kubectl get applications` is already the
  plural for ArgoCD's own `Application` CRD (`applications.argoproj.io`) —
  registering a second CRD that also pluralizes to `applications` (ours would
  be `applications.access.cloudflare.crossplane.io`) makes `kubectl get
  application`/`applications` ambiguous, and it was observed resolving to the
  wrong one on the actual cluster. Renamed before any real resource existed to
  avoid ever having a live collision.

## Build & test locally

```sh
make generate   # regenerates zz_generated.deepcopy.go and package/crds/*.yaml
make build
make test
```

## Releasing

Releases are built by the repository's [build workflow](../.github/workflows/build.yaml); see the
[top-level README](../README.md#release-process).

## ProviderConfig

Its own kind at `access.cloudflare.crossplane.io/v1alpha1`, referencing a Secret
holding a Cloudflare API Token scoped to at least "Access: Apps and Policies: Edit".
