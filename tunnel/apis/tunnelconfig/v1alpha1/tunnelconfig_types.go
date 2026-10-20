package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IngressRule maps a public hostname to a local service behind a tunnel. The
// last rule in the list must be a catch-all with no hostname (e.g. only
// service: "http_status:404") — Cloudflare requires it.
type IngressRule struct {
	// Hostname this rule matches. Omit only for the mandatory trailing
	// catch-all rule.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// Service is the destination cloudflared proxies matching requests to,
	// e.g. "http://localhost:8080", or a literal "http_status:404" for the
	// catch-all rule.
	// +kubebuilder:validation:Required
	Service string `json:"service"`

	// Path, if set, further restricts this rule to requests under it.
	// +optional
	Path string `json:"path,omitempty"`

	// NoTLSVerify disables TLS certificate verification for HTTPS origins —
	// use when the origin serves a self-signed or internally-issued
	// certificate cloudflared wouldn't otherwise trust.
	// +optional
	NoTLSVerify bool `json:"noTLSVerify,omitempty"`
}

// TunnelConfigParameters define the desired ingress configuration of a
// remotely-managed Cloudflare Tunnel.
type TunnelConfigParameters struct {
	// AccountID is the Cloudflare account the tunnel belongs to. Must match
	// the referenced Tunnel's accountId.
	// +kubebuilder:validation:Required
	AccountID string `json:"accountId"`

	// TunnelID is the Cloudflare tunnel this configuration applies to. Either
	// this or tunnelIdRef/tunnelIdSelector must resolve to a value.
	// Immutable after creation — a TunnelConfig is 1:1 with its Tunnel.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="tunnelId is immutable"
	TunnelID string `json:"tunnelId,omitempty"`

	// TunnelIDRef references a Tunnel to resolve tunnelId from.
	// +optional
	TunnelIDRef *xpv1.Reference `json:"tunnelIdRef,omitempty"`

	// TunnelIDSelector selects a Tunnel to resolve tunnelId from.
	// +optional
	TunnelIDSelector *xpv1.Selector `json:"tunnelIdSelector,omitempty"`

	// Ingress is the ordered list of public hostname routing rules. At least
	// one rule is required; the last must be a catch-all (no hostname).
	// +kubebuilder:validation:MinItems=1
	Ingress []IngressRule `json:"ingress"`
}

// TunnelConfigObservation reflects the observed state of a tunnel's ingress configuration.
type TunnelConfigObservation struct {
	// Version is the configuration revision number Cloudflare reports.
	Version int64 `json:"version,omitempty"`
}

// A TunnelConfigSpec defines the desired state of a TunnelConfig.
type TunnelConfigSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       TunnelConfigParameters `json:"forProvider"`
}

// A TunnelConfigStatus reflects the observed state of a TunnelConfig.
type TunnelConfigStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          TunnelConfigObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="TUNNEL",type="string",JSONPath=".spec.forProvider.tunnelId"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,cloudflare}

// A TunnelConfig is a managed resource representing a Cloudflare Tunnel's
// remotely-managed ingress configuration.
type TunnelConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TunnelConfigSpec   `json:"spec"`
	Status TunnelConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TunnelConfigList contains a list of TunnelConfig.
type TunnelConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TunnelConfig `json:"items"`
}

// GetItems of this TunnelConfigList, satisfying resource.ManagedList (required
// for cross-resource reference selectors to enumerate candidates).
func (l *TunnelConfigList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// GetCondition of this TunnelConfig.
func (t *TunnelConfig) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return t.Status.GetCondition(ct)
}

// SetConditions of this TunnelConfig.
func (t *TunnelConfig) SetConditions(c ...xpv1.Condition) {
	t.Status.SetConditions(c...)
}

// GetProviderConfigReference of this TunnelConfig.
func (t *TunnelConfig) GetProviderConfigReference() *xpv1.Reference {
	return t.Spec.ProviderConfigReference
}

// SetProviderConfigReference of this TunnelConfig.
func (t *TunnelConfig) SetProviderConfigReference(r *xpv1.Reference) {
	t.Spec.ProviderConfigReference = r
}

// GetWriteConnectionSecretToReference of this TunnelConfig.
func (t *TunnelConfig) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return t.Spec.WriteConnectionSecretToReference
}

// SetWriteConnectionSecretToReference of this TunnelConfig.
func (t *TunnelConfig) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	t.Spec.WriteConnectionSecretToReference = r
}

// GetPublishConnectionDetailsTo of this TunnelConfig.
func (t *TunnelConfig) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return t.Spec.PublishConnectionDetailsTo
}

// SetPublishConnectionDetailsTo of this TunnelConfig.
func (t *TunnelConfig) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	t.Spec.PublishConnectionDetailsTo = r
}

// GetDeletionPolicy of this TunnelConfig.
func (t *TunnelConfig) GetDeletionPolicy() xpv1.DeletionPolicy {
	return t.Spec.DeletionPolicy
}

// SetDeletionPolicy of this TunnelConfig.
func (t *TunnelConfig) SetDeletionPolicy(policy xpv1.DeletionPolicy) {
	t.Spec.DeletionPolicy = policy
}

// GetManagementPolicies of this TunnelConfig.
func (t *TunnelConfig) GetManagementPolicies() xpv1.ManagementPolicies {
	return t.Spec.ManagementPolicies
}

// SetManagementPolicies of this TunnelConfig.
func (t *TunnelConfig) SetManagementPolicies(policies xpv1.ManagementPolicies) {
	t.Spec.ManagementPolicies = policies
}
