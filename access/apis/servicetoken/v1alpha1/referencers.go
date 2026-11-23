package v1alpha1

import (
	"github.com/crossplane/crossplane-runtime/pkg/reference"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
)

// ServiceTokenID extracts a ServiceToken's Cloudflare ID for use by Policy
// rules that reference a ServiceToken by its Kubernetes object name.
func ServiceTokenID() reference.ExtractValueFn {
	return func(mg resource.Managed) string {
		t, ok := mg.(*ServiceToken)
		if !ok {
			return ""
		}
		return t.Status.AtProvider.ID
	}
}
