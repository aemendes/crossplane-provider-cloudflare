package v1alpha1

import (
	"github.com/crossplane/crossplane-runtime/pkg/reference"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
)

// TunnelID extracts a Tunnel's Cloudflare tunnel ID for use by other
// resources (TunnelConfig, DNSRecord) that reference a Tunnel by its
// Kubernetes object name.
func TunnelID() reference.ExtractValueFn {
	return func(mg resource.Managed) string {
		t, ok := mg.(*Tunnel)
		if !ok {
			return ""
		}
		return t.Status.AtProvider.ID
	}
}
