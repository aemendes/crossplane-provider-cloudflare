// Package apis contains Kubernetes API for the Cloudflare Access provider.
package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	accessapplicationv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/accessapplication/v1alpha1"
	policyv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/policy/v1alpha1"
	cloudflarev1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/v1alpha1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme.
var AddToSchemes runtime.SchemeBuilder = runtime.SchemeBuilder{
	cloudflarev1alpha1.SchemeBuilder.AddToScheme,
	policyv1alpha1.SchemeBuilder.AddToScheme,
	accessapplicationv1alpha1.SchemeBuilder.AddToScheme,
}

// AddToScheme adds all Resources to the Scheme.
func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}
