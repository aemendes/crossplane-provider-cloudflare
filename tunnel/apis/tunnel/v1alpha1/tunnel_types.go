package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TunnelParameters define the desired state of a Cloudflare Tunnel.
type TunnelParameters struct {
	// AccountID is the Cloudflare account this tunnel belongs to. Immutable
	// after creation — Cloudflare Tunnels cannot move between accounts.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accountId is immutable"
	AccountID string `json:"accountId"`

	// Name of the tunnel, shown in the Zero Trust dashboard. Falls back to
	// this resource's own Kubernetes name if unset. Cloudflare rejects
	// duplicate tunnel names within an account.
	// +optional
	Name string `json:"name,omitempty"`
}

// TunnelObservation reflects the observed state of a Cloudflare Tunnel.
type TunnelObservation struct {
	// ID is the Cloudflare tunnel ID (UUID).
	ID string `json:"id,omitempty"`

	// Status is the tunnel's connectivity status as reported by Cloudflare:
	// inactive, degraded, healthy, or down.
	Status string `json:"status,omitempty"`

	// CreatedAt is when the tunnel was created, per the Cloudflare API.
	CreatedAt string `json:"createdAt,omitempty"`
}

// A TunnelSpec defines the desired state of a Tunnel.
type TunnelSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       TunnelParameters `json:"forProvider"`
}

// A TunnelStatus reflects the observed state of a Tunnel.
type TunnelStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          TunnelObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.atProvider.id"
// +kubebuilder:printcolumn:name="STATUS",type="string",JSONPath=".status.atProvider.status"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,cloudflare}

// A Tunnel is a managed resource representing a Cloudflare Tunnel.
type Tunnel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TunnelSpec   `json:"spec"`
	Status TunnelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TunnelList contains a list of Tunnel.
type TunnelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tunnel `json:"items"`
}

// GetItems of this TunnelList, satisfying resource.ManagedList (required for
// cross-resource reference selectors to enumerate candidates).
func (l *TunnelList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// GetCondition of this Tunnel.
func (t *Tunnel) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return t.Status.GetCondition(ct)
}

// SetConditions of this Tunnel.
func (t *Tunnel) SetConditions(c ...xpv1.Condition) {
	t.Status.SetConditions(c...)
}

// GetProviderConfigReference of this Tunnel.
func (t *Tunnel) GetProviderConfigReference() *xpv1.Reference {
	return t.Spec.ProviderConfigReference
}

// SetProviderConfigReference of this Tunnel.
func (t *Tunnel) SetProviderConfigReference(r *xpv1.Reference) {
	t.Spec.ProviderConfigReference = r
}

// GetWriteConnectionSecretToReference of this Tunnel.
func (t *Tunnel) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return t.Spec.WriteConnectionSecretToReference
}

// SetWriteConnectionSecretToReference of this Tunnel.
func (t *Tunnel) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	t.Spec.WriteConnectionSecretToReference = r
}

// GetPublishConnectionDetailsTo of this Tunnel.
func (t *Tunnel) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return t.Spec.PublishConnectionDetailsTo
}

// SetPublishConnectionDetailsTo of this Tunnel.
func (t *Tunnel) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	t.Spec.PublishConnectionDetailsTo = r
}

// GetDeletionPolicy of this Tunnel.
func (t *Tunnel) GetDeletionPolicy() xpv1.DeletionPolicy {
	return t.Spec.DeletionPolicy
}

// SetDeletionPolicy of this Tunnel.
func (t *Tunnel) SetDeletionPolicy(policy xpv1.DeletionPolicy) {
	t.Spec.DeletionPolicy = policy
}

// GetManagementPolicies of this Tunnel.
func (t *Tunnel) GetManagementPolicies() xpv1.ManagementPolicies {
	return t.Spec.ManagementPolicies
}

// SetManagementPolicies of this Tunnel.
func (t *Tunnel) SetManagementPolicies(policies xpv1.ManagementPolicies) {
	t.Spec.ManagementPolicies = policies
}
