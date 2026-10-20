package v1alpha1

import (
	"github.com/crossplane/crossplane-runtime/pkg/reference"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
)

// PolicyID extracts a Policy's Cloudflare policy ID for use by other
// resources (Application) that reference a Policy by its Kubernetes object name.
func PolicyID() reference.ExtractValueFn {
	return func(mg resource.Managed) string {
		p, ok := mg.(*Policy)
		if !ok {
			return ""
		}
		return p.Status.AtProvider.ID
	}
}
