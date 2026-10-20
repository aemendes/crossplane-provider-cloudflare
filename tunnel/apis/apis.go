// Package apis contains Kubernetes API for the Cloudflare Tunnel provider.
package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	dnsrecordv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/dnsrecord/v1alpha1"
	tunnelv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnel/v1alpha1"
	tunnelconfigv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnelconfig/v1alpha1"
	cloudflarev1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/v1alpha1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme.
var AddToSchemes runtime.SchemeBuilder = runtime.SchemeBuilder{
	cloudflarev1alpha1.SchemeBuilder.AddToScheme,
	tunnelv1alpha1.SchemeBuilder.AddToScheme,
	tunnelconfigv1alpha1.SchemeBuilder.AddToScheme,
	dnsrecordv1alpha1.SchemeBuilder.AddToScheme,
}

// AddToScheme adds all Resources to the Scheme.
func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}
