#!/usr/bin/env bash
# Scaffolds a new small, single-purpose Cloudflare Crossplane provider, following
# the pattern established by tunnel/: its own Go
# module, its own ProviderConfig at <name>.cloudflare.crossplane.io (never a
# shared top-level group — two Crossplane packages can't own the same CRD),
# and its own entry in the build matrix (.github/workflows/build.yaml).
#
# This stamps out the generic ~90% (ProviderConfig plumbing, main.go, Makefile,
# Dockerfile, package metadata) that is identical across every
# Cloudflare-scoped provider. It deliberately does NOT stamp out a shared
# internal/clients/cloudflare client — only ~30 lines of that (NewClient,
# IsNotFound, IsRateLimited) are actually shareable, and the rest is
# domain-specific per provider. A stub is generated with just those helpers;
# fill in the domain methods, apis/<kind>/v1alpha1 types, and
# internal/controller/<kind> by hand, using provider-cloudflare-tunnel as a
# reference (see its README's "Layout" section).
#
# Usage: scripts/new-provider.sh <name> "<description>"
# Example: scripts/new-provider.sh access "Zero Trust Access applications and policies"

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <name> \"<description>\"" >&2
  echo 'Example: $0 access "Zero Trust Access applications and policies"' >&2
  exit 1
fi

NAME="$1"           # e.g. "access" -> provider-cloudflare-access, access.cloudflare.crossplane.io
DESCRIPTION="$2"    # human-readable, used in crossplane.yaml + README

if [[ ! "$NAME" =~ ^[a-z][a-z0-9-]*$ ]]; then
  echo "error: <name> must be lowercase alphanumeric/hyphens, e.g. 'access' or 'load-balancer'" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROVIDER_DIR="$REPO_ROOT/${NAME}"
MODULE="github.com/aemendes/crossplane-provider-cloudflare/${NAME}"
GROUP="${NAME}.cloudflare.crossplane.io"
TITLE="$(echo "${NAME:0:1}" | tr '[:lower:]' '[:upper:]')${NAME:1}"

if [[ -e "$PROVIDER_DIR" ]]; then
  echo "error: $PROVIDER_DIR already exists" >&2
  exit 1
fi

echo "Scaffolding provider-cloudflare-${NAME} at $PROVIDER_DIR ..."
mkdir -p "$PROVIDER_DIR"/{apis/v1alpha1,internal/controller/providerconfig,internal/clients/cloudflare,cmd/provider,package/crds,examples,.bin}

# ---- go.mod ----
cat > "$PROVIDER_DIR/go.mod" <<EOF
module ${MODULE}

go 1.26.4
EOF

# ---- apis/apis.go ----
cat > "$PROVIDER_DIR/apis/apis.go" <<EOF
// Package apis contains Kubernetes API for the Cloudflare ${TITLE} provider.
package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	cloudflarev1alpha1 "${MODULE}/apis/v1alpha1"
	// TODO: import your <kind>/v1alpha1 packages here as you add them, e.g.:
	// examplev1alpha1 "${MODULE}/apis/example/v1alpha1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme.
var AddToSchemes runtime.SchemeBuilder = runtime.SchemeBuilder{
	cloudflarev1alpha1.SchemeBuilder.AddToScheme,
	// TODO: examplev1alpha1.SchemeBuilder.AddToScheme,
}

// AddToScheme adds all Resources to the Scheme.
func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}
EOF

# ---- apis/v1alpha1 (ProviderConfig) ----
cat > "$PROVIDER_DIR/apis/v1alpha1/doc.go" <<EOF
// Package v1alpha1 contains the ProviderConfig API for the Cloudflare ${TITLE} provider.
// +kubebuilder:object:generate=true
// +groupName=${GROUP}
// +versionName=v1alpha1
package v1alpha1
EOF

cat > "$PROVIDER_DIR/apis/v1alpha1/providerconfig_types.go" <<'EOF'
package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A ProviderConfigSpec defines the desired state of a ProviderConfig.
type ProviderConfigSpec struct {
	// Credentials required to authenticate to the Cloudflare API. The
	// referenced secret value must be a Cloudflare API Token (not a legacy API
	// key) scoped to whatever this provider's controllers need.
	Credentials ProviderCredentials `json:"credentials"`
}

// ProviderCredentials required to authenticate to the Cloudflare API.
type ProviderCredentials struct {
	// Source of the credentials.
	// +kubebuilder:validation:Enum=None;Secret;InjectedIdentity;Environment;Filesystem
	Source xpv1.CredentialsSource `json:"source"`

	xpv1.CommonCredentialSelectors `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// A ProviderConfig configures a Cloudflare provider.
type ProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderConfigSpec        `json:"spec"`
	Status xpv1.ProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProviderConfigList contains a list of ProviderConfig.
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

// GetCondition of this ProviderConfig.
func (p *ProviderConfig) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return p.Status.GetCondition(ct)
}

// SetConditions of this ProviderConfig.
func (p *ProviderConfig) SetConditions(c ...xpv1.Condition) {
	p.Status.SetConditions(c...)
}

// GetUsers of this ProviderConfig.
func (p *ProviderConfig) GetUsers() int64 {
	return p.Status.Users
}

// SetUsers of this ProviderConfig.
func (p *ProviderConfig) SetUsers(i int64) {
	p.Status.Users = i
}
EOF

cat > "$PROVIDER_DIR/apis/v1alpha1/providerconfigusage_types.go" <<'EOF'
package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="CONFIG-NAME",type="string",JSONPath=".providerConfigRef.name"
// +kubebuilder:printcolumn:name="RESOURCE-KIND",type="string",JSONPath=".resourceRef.kind"
// +kubebuilder:printcolumn:name="RESOURCE-NAME",type="string",JSONPath=".resourceRef.name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// A ProviderConfigUsage indicates that a resource is using a ProviderConfig.
type ProviderConfigUsage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	xpv1.ProviderConfigUsage `json:",inline"`
}

// +kubebuilder:object:root=true

// ProviderConfigUsageList contains a list of ProviderConfigUsage.
type ProviderConfigUsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfigUsage `json:"items"`
}

// GetProviderConfigReference of this ProviderConfigUsage.
func (p *ProviderConfigUsage) GetProviderConfigReference() xpv1.Reference {
	return p.ProviderConfigUsage.ProviderConfigReference
}

// SetProviderConfigReference of this ProviderConfigUsage.
func (p *ProviderConfigUsage) SetProviderConfigReference(r xpv1.Reference) {
	p.ProviderConfigUsage.ProviderConfigReference = r
}

// GetResourceReference of this ProviderConfigUsage.
func (p *ProviderConfigUsage) GetResourceReference() xpv1.TypedReference {
	return p.ProviderConfigUsage.ResourceReference
}

// SetResourceReference of this ProviderConfigUsage.
func (p *ProviderConfigUsage) SetResourceReference(r xpv1.TypedReference) {
	p.ProviderConfigUsage.ResourceReference = r
}

// GetItems of this ProviderConfigUsageList.
func (p *ProviderConfigUsageList) GetItems() []resource.ProviderConfigUsage {
	items := make([]resource.ProviderConfigUsage, len(p.Items))
	for i := range p.Items {
		items[i] = &p.Items[i]
	}
	return items
}
EOF

cat > "$PROVIDER_DIR/apis/v1alpha1/register.go" <<EOF
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"

	"github.com/crossplane/crossplane-runtime/pkg/resource"
)

// Compile-time checks that these types satisfy the crossplane-runtime interfaces
// the reconcilers require. Without these, a missing method surfaces only as a
// runtime panic on provider startup.
var (
	_ resource.ProviderConfig          = &ProviderConfig{}
	_ resource.ProviderConfigUsage     = &ProviderConfigUsage{}
	_ resource.ProviderConfigUsageList = &ProviderConfigUsageList{}
)

// Package type metadata.
//
// This lives at its own sub-group rather than a shared top-level
// "cloudflare.crossplane.io" — Crossplane packages can't share ownership of a
// CRD, so a sibling Cloudflare-scoped provider registering its own
// ProviderConfig at a shared group would collide. Each such provider gets its
// own ProviderConfig at <name>.cloudflare.crossplane.io, all independently
// referencing whatever Secret holds the API token.
const (
	Group   = "${GROUP}"
	Version = "v1alpha1"
)

var (
	// SchemeGroupVersion is group version used to register these objects.
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// ProviderConfigKind takes an unqualified kind and returns back a Group qualified GroupKind.
func ProviderConfigKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: Group, Kind: kind}
}

// ProviderConfigGroupKind is the root Kind for the ProviderConfig type, used to derive a controller name.
const ProviderConfigGroupKind = "ProviderConfig." + Group

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
	SchemeBuilder.Register(&ProviderConfigUsage{}, &ProviderConfigUsageList{})
}
EOF

# ---- internal/clients/cloudflare (stub: only the genuinely shareable bits) ----
cat > "$PROVIDER_DIR/internal/clients/cloudflare/client.go" <<EOF
// Package cloudflare is a thin wrapper around cloudflare-go/v7, scoped to the
// operations this provider needs. It is not a general-purpose Cloudflare client.
//
// TODO: add domain-specific methods here (Create/Get/List/Update/Delete against
// whichever cloudflare-go/v7 service this provider targets), following the
// pattern in tunnel/internal/clients/cloudflare/client.go —
// e.g. wrap error handling per-resource, return small domain-shaped structs
// instead of the raw SDK response types.
package cloudflare

import (
	"errors"
	"net/http"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

// Client wraps the official Cloudflare SDK, authenticated with an API token.
type Client struct {
	sdk *cf.Client
}

// NewClient returns a Client authenticated with the supplied Cloudflare API token.
func NewClient(apiToken string) *Client {
	return &Client{sdk: cf.NewClient(option.WithAPIToken(apiToken))}
}

// NewClientWithBaseURL returns a Client pointed at a custom base URL, for use in tests.
func NewClientWithBaseURL(apiToken, baseURL string) *Client {
	return &Client{sdk: cf.NewClient(option.WithAPIToken(apiToken), option.WithBaseURL(baseURL))}
}

// IsNotFound returns true if the supplied error is a Cloudflare 404 response.
func IsNotFound(err error) bool {
	var apiErr *cf.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsRateLimited returns true if the supplied error is a Cloudflare 429 response.
func IsRateLimited(err error) bool {
	var apiErr *cf.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

// TODO: add domain methods here using c.sdk.<Service>..., e.g.:
//
// func (c *Client) GetWidget(ctx context.Context, id string) (*Widget, error) {
// 	w, err := c.sdk.Example.Widgets.Get(ctx, id, example.WidgetGetParams{...})
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &Widget{ID: w.ID, Name: w.Name}, nil
// }
EOF

# ---- internal/controller/providerconfig ----
cat > "$PROVIDER_DIR/internal/controller/providerconfig/providerconfig.go" <<EOF
// Package providerconfig reconciles ProviderConfigs by tracking their usage.
package providerconfig

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	xpcontroller "github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/providerconfig"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"${MODULE}/apis/v1alpha1"
)

// Setup adds a controller that reconciles ProviderConfigs by accounting for
// their current usage.
func Setup(mgr ctrl.Manager, o xpcontroller.Options) error {
	name := providerconfig.ControllerName(v1alpha1.ProviderConfigGroupKind)

	of := resource.ProviderConfigKinds{
		Config:    v1alpha1.SchemeGroupVersion.WithKind("ProviderConfig"),
		Usage:     v1alpha1.SchemeGroupVersion.WithKind("ProviderConfigUsage"),
		UsageList: v1alpha1.SchemeGroupVersion.WithKind("ProviderConfigUsageList"),
	}

	r := providerconfig.NewReconciler(mgr, of,
		providerconfig.WithLogger(o.Logger.WithValues("controller", name)),
		providerconfig.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.ProviderConfig{}).
		Watches(&v1alpha1.ProviderConfigUsage{}, &resource.EnqueueRequestForProviderConfig{}).
		Complete(r)
}
EOF

# ---- internal/controller/controller.go ----
cat > "$PROVIDER_DIR/internal/controller/controller.go" <<EOF
// Package controller aggregates the controllers for this provider.
package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	xpcontroller "github.com/crossplane/crossplane-runtime/pkg/controller"

	"${MODULE}/internal/controller/providerconfig"
	// TODO: import your <kind> controller packages here as you add them, e.g.:
	// "${MODULE}/internal/controller/example"
)

// Setup creates all controllers with the supplied logger and adds them to the supplied manager.
func Setup(mgr ctrl.Manager, o xpcontroller.Options) error {
	for _, setup := range []func(ctrl.Manager, xpcontroller.Options) error{
		providerconfig.Setup,
		// TODO: example.Setup,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}
EOF

# ---- cmd/provider/main.go ----
cat > "$PROVIDER_DIR/cmd/provider/main.go" <<EOF
// Command provider runs the Cloudflare ${TITLE} Crossplane provider.
package main

import (
	"flag"
	"log"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	zaplog "sigs.k8s.io/controller-runtime/pkg/log/zap"

	xpcontroller "github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"

	"${MODULE}/apis"
	"${MODULE}/internal/controller"
)

func main() {
	var (
		debug            = flag.Bool("debug", false, "Run with debug logging.")
		syncPeriod       = flag.Duration("sync", time.Hour, "Controller manager sync period, e.g. 1h.")
		pollInterval     = flag.Duration("poll", time.Minute, "How often an individual resource is checked for drift.")
		leaderElection   = flag.Bool("leader-election", false, "Use leader election for the controller manager.")
		maxReconcileRate = flag.Int("max-reconcile-rate", 10, "Global maximum rate per second at which resources may be checked for drift.")
		healthAddr       = flag.String("health-addr", ":8081", "Address at which to serve the /healthz liveness endpoint.")
	)
	flag.Parse()

	zl := zaplog.New(zaplog.UseDevMode(*debug))
	logr := logging.NewLogrLogger(zl.WithName("provider-cloudflare-${NAME}"))
	// Always set the controller-runtime logger, not just in debug mode — otherwise
	// it warns "logger was never set" and silently swallows its own internal errors
	// (watch failures, cache issues) rather than just being quieter.
	ctrl.SetLogger(zl)

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("cannot get API server rest config: %v", err)
	}

	mgr, err := ctrl.NewManager(ratelimiter.LimitRESTConfig(cfg, *maxReconcileRate), ctrl.Options{
		Cache:                  cache.Options{SyncPeriod: syncPeriod},
		LeaderElection:         *leaderElection,
		LeaderElectionID:       "crossplane-leader-election-provider-cloudflare-${NAME}",
		HealthProbeBindAddress: *healthAddr,
	})
	if err != nil {
		log.Fatalf("cannot create controller manager: %v", err)
	}

	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Fatalf("cannot add Cloudflare ${TITLE} APIs to scheme: %v", err)
	}

	o := xpcontroller.Options{
		Logger:                  logr,
		GlobalRateLimiter:       ratelimiter.NewGlobal(*maxReconcileRate),
		PollInterval:            *pollInterval,
		MaxConcurrentReconciles: *maxReconcileRate,
	}

	if err := controller.Setup(mgr, o); err != nil {
		log.Fatalf("cannot setup Cloudflare ${TITLE} controllers: %v", err)
	}

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		log.Fatalf("cannot create health check: %v", err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("cannot start controller manager: %v", err)
	}
}
EOF

# ---- Dockerfile ----
cat > "$PROVIDER_DIR/Dockerfile" <<'EOF'
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Cross-compile natively on the build platform (fast) rather than relying on qemu
# to emulate the Go toolchain itself when --platform != the build host's arch.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o /out/provider ./cmd/provider

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/provider /usr/local/bin/provider
USER nonroot:nonroot
ENTRYPOINT ["provider"]
EOF

# ---- Makefile ----
cat > "$PROVIDER_DIR/Makefile" <<EOF
IMAGE ?= ghcr.io/aemendes/provider-cloudflare-${NAME}:latest
PKG_IMAGE ?= ghcr.io/aemendes/provider-cloudflare-${NAME}-pkg:latest
PLATFORM ?= linux/amd64
CONTROLLER_GEN ?= .bin/controller-gen

.PHONY: controller-gen
controller-gen:
	GOBIN=\$(CURDIR)/.bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5

.PHONY: generate
generate: controller-gen
	\$(CONTROLLER_GEN) object:headerFile="" paths="./..."
	\$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=package/crds

.PHONY: build
build: generate
	go build ./...

.PHONY: test
test: generate
	go test ./...

.PHONY: docker-build
docker-build:
	docker buildx build --platform \$(PLATFORM) -t \$(IMAGE) --load .

.PHONY: docker-push
docker-push: docker-build
	docker push \$(IMAGE)

.PHONY: xpkg-build
xpkg-build: docker-build
	crossplane xpkg build --package-root=package --embed-runtime-image=\$(IMAGE) -o provider-cloudflare-${NAME}.xpkg

.PHONY: xpkg-push
xpkg-push: xpkg-build
	crossplane xpkg push -f provider-cloudflare-${NAME}.xpkg \$(PKG_IMAGE)
EOF

# ---- package/crossplane.yaml ----
cat > "$PROVIDER_DIR/package/crossplane.yaml" <<EOF
apiVersion: meta.pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-cloudflare-${NAME}
  annotations:
    meta.crossplane.io/maintainer: Alexandre Mendes
    meta.crossplane.io/source: github.com/aemendes/crossplane-provider-cloudflare
    meta.crossplane.io/description: |
      Native Go Crossplane provider for Cloudflare ${TITLE}: ${DESCRIPTION}.
      Scoped to this feature only — not a general-purpose Cloudflare provider.
EOF

# ---- .gitignore ----
cat > "$PROVIDER_DIR/.gitignore" <<'EOF'
.bin/
*.xpkg
EOF

# ---- examples/providerconfig.yaml ----
cat > "$PROVIDER_DIR/examples/providerconfig.yaml" <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: cloudflare-${NAME}-creds
  namespace: crossplane-system
type: Opaque
stringData:
  credentials: REPLACE_WITH_CLOUDFLARE_API_TOKEN
---
apiVersion: ${GROUP}/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      namespace: crossplane-system
      name: cloudflare-${NAME}-creds
      key: credentials
EOF

# ---- README.md ----
cat > "$PROVIDER_DIR/README.md" <<EOF
# provider-cloudflare-${NAME}

A native Go Crossplane provider for Cloudflare ${TITLE}: ${DESCRIPTION}.
Scoped to this feature only — not a general-purpose Cloudflare provider.

Scaffolded by \`scripts/new-provider.sh\`; see
\`tunnel/README.md\` for the sibling provider this
pattern is modeled on.

## TODO

- [ ] Design and add \`apis/<kind>/v1alpha1\` managed resource type(s)
- [ ] Add domain methods to \`internal/clients/cloudflare/client.go\`
- [ ] Add \`internal/controller/<kind>\` Observe/Create/Update/Delete controller(s)
- [ ] Wire new controller(s) into \`internal/controller/controller.go\`
- [ ] Wire new scheme(s) into \`apis/apis.go\`
- [ ] \`make generate && make build && make test\`
- [ ] Add \`${NAME}\` to the filters and workflow_dispatch options in \`.github/workflows/build.yaml\`
- [ ] Add the provider to the table and resource reference in the top-level README.md

## ProviderConfig

Its own kind at \`${GROUP}/v1alpha1\`, referencing a Secret holding a Cloudflare
API Token scoped to whatever this provider's controllers need.
EOF

# Pin the same dependency versions as every sibling Cloudflare provider so the
# family doesn't drift apart version-by-version as it grows (crossplane-runtime
# v1.20.10 / controller-runtime v0.19.0 / apimachinery+client-go v0.31.0 match
# provider-cloudflare-tunnel and provider-cloudflare-access as of this writing — bump here
# deliberately, in one place, if the family ever needs a coordinated upgrade).
echo "Pinning shared dependency versions..."
(cd "$PROVIDER_DIR" && go get \
  github.com/crossplane/crossplane-runtime@v1.20.10 \
  sigs.k8s.io/controller-runtime@v0.19.0 \
  k8s.io/apimachinery@v0.31.0 \
  k8s.io/client-go@v0.31.0 \
  github.com/cloudflare/cloudflare-go/v7@v7.7.0)

echo "Done. Next steps:"
echo "  1. cd ${NAME} && go mod tidy"
echo "  2. Design apis/<kind>/v1alpha1 types and internal/controller/<kind>, referencing provider-cloudflare-tunnel"
echo "  3. make generate && make build && make test"
echo "  4. Add ${NAME} to the matrix in .github/workflows/build.yaml (filters + workflow_dispatch options)"
echo "  5. Add ${NAME} to the provider table in README.md"
