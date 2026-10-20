package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DNSRecordParameters define the desired state of a hostname routed to a
// Cloudflare Tunnel. This provider only creates the CNAME record a tunnel
// needs (target "<tunnelId>.cfargotunnel.com") — it is not a general-purpose
// Cloudflare DNS record resource.
type DNSRecordParameters struct {
	// ZoneID is the Cloudflare zone this record is created in. Immutable
	// after creation — Cloudflare does not support moving a DNS record
	// between zones.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="zoneId is immutable"
	ZoneID string `json:"zoneId"`

	// Name is the hostname to route to the tunnel, e.g. "app.example.com".
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// TunnelID is the tunnel this hostname routes to. Either this or
	// tunnelIdRef/tunnelIdSelector must resolve to a value.
	// +optional
	TunnelID string `json:"tunnelId,omitempty"`

	// TunnelIDRef references a Tunnel to resolve tunnelId from.
	// +optional
	TunnelIDRef *xpv1.Reference `json:"tunnelIdRef,omitempty"`

	// TunnelIDSelector selects a Tunnel to resolve tunnelId from.
	// +optional
	TunnelIDSelector *xpv1.Selector `json:"tunnelIdSelector,omitempty"`

	// Proxied determines whether traffic is proxied through Cloudflare
	// (orange-clouded). Almost always required for a tunnel hostname to work.
	// +optional
	// +kubebuilder:default=true
	Proxied bool `json:"proxied,omitempty"`
}

// DNSRecordObservation reflects the observed state of a DNS record.
type DNSRecordObservation struct {
	// ID is the Cloudflare DNS record ID.
	ID string `json:"id,omitempty"`

	// Content is the CNAME target Cloudflare reports for this record.
	Content string `json:"content,omitempty"`
}

// A DNSRecordSpec defines the desired state of a DNSRecord.
type DNSRecordSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       DNSRecordParameters `json:"forProvider"`
}

// A DNSRecordStatus reflects the observed state of a DNSRecord.
type DNSRecordStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          DNSRecordObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="HOSTNAME",type="string",JSONPath=".spec.forProvider.name"
// +kubebuilder:printcolumn:name="TUNNEL",type="string",JSONPath=".spec.forProvider.tunnelId"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,cloudflare}

// A DNSRecord is a managed resource representing the CNAME record that routes
// a hostname to a Cloudflare Tunnel.
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSRecordSpec   `json:"spec"`
	Status DNSRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DNSRecordList contains a list of DNSRecord.
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}

// GetItems of this DNSRecordList, satisfying resource.ManagedList (required
// for cross-resource reference selectors to enumerate candidates).
func (l *DNSRecordList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// GetCondition of this DNSRecord.
func (d *DNSRecord) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return d.Status.GetCondition(ct)
}

// SetConditions of this DNSRecord.
func (d *DNSRecord) SetConditions(c ...xpv1.Condition) {
	d.Status.SetConditions(c...)
}

// GetProviderConfigReference of this DNSRecord.
func (d *DNSRecord) GetProviderConfigReference() *xpv1.Reference {
	return d.Spec.ProviderConfigReference
}

// SetProviderConfigReference of this DNSRecord.
func (d *DNSRecord) SetProviderConfigReference(r *xpv1.Reference) {
	d.Spec.ProviderConfigReference = r
}

// GetWriteConnectionSecretToReference of this DNSRecord.
func (d *DNSRecord) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return d.Spec.WriteConnectionSecretToReference
}

// SetWriteConnectionSecretToReference of this DNSRecord.
func (d *DNSRecord) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	d.Spec.WriteConnectionSecretToReference = r
}

// GetPublishConnectionDetailsTo of this DNSRecord.
func (d *DNSRecord) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return d.Spec.PublishConnectionDetailsTo
}

// SetPublishConnectionDetailsTo of this DNSRecord.
func (d *DNSRecord) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	d.Spec.PublishConnectionDetailsTo = r
}

// GetDeletionPolicy of this DNSRecord.
func (d *DNSRecord) GetDeletionPolicy() xpv1.DeletionPolicy {
	return d.Spec.DeletionPolicy
}

// SetDeletionPolicy of this DNSRecord.
func (d *DNSRecord) SetDeletionPolicy(policy xpv1.DeletionPolicy) {
	d.Spec.DeletionPolicy = policy
}

// GetManagementPolicies of this DNSRecord.
func (d *DNSRecord) GetManagementPolicies() xpv1.ManagementPolicies {
	return d.Spec.ManagementPolicies
}

// SetManagementPolicies of this DNSRecord.
func (d *DNSRecord) SetManagementPolicies(policies xpv1.ManagementPolicies) {
	d.Spec.ManagementPolicies = policies
}
