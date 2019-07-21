package admissioncontrol

import (
	"fmt"

	admission "k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

// CloudProvider represents supported cloud platforms for provider-specific
// configuration.
type CloudProvider int

const (
	// GCP is a constant for Google Cloud Platform specific logic.
	GCP CloudProvider = iota
	// Azure is a constant for cloud-specific logic.
	Azure
	// AWS is a constant for Amazon Web Services specific logic.
	AWS
	// OpenStack is a constant for cloud-specific logic.
	OpenStack
)

// ilbAnnotations maps the annotation key:value pairs required to denote an internal-only load balancer on the supported cloud platforms.
//
// Docs: https://kubernetes.io/docs/concepts/services-networking/#internal-load-balancer
var ilbAnnotations = map[CloudProvider]map[string]string{
	GCP:       {"cloud.google.com/load-balancer-type": "Internal"},
	Azure:     {"service.beta.kubernetes.io/azure-load-balancer-internal": "true"},
	AWS:       {"service.beta.kubernetes.io/aws-load-balancer-internal": "0.0.0.0/0"},
	OpenStack: {"service.beta.kubernetes.io/openstack-internal-load-balancer": "true"},
}

// newDefaultDenyResponse returns an AdmissionResponse that defaults to allowed
// = false, and creates sub-objects.
func newDefaultDenyResponse() *admission.AdmissionResponse {
	return &admission.AdmissionResponse{
		Allowed: false,
		Result:  &metav1.Status{},
	}
}

// DenyIngresses denies any kind: Ingress from being deployed to the cluster,
// except for any explicitly allowed namespaces (e.g. istio-system).
//
// Providing an empty/nil list of allowedNamespaces will reject Ingress objects
// across all namespaces. Kinds other than Ingress will be allowed.
func DenyIngresses(allowedNamespaces []string) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind // Base Kind - e.g. "Service" as opposed to "v1/Service"
		resp := newDefaultDenyResponse()

		switch kind {
		case "Ingress":
			ingress := extensionsv1beta1.Ingress{}
			deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &ingress); err != nil {
				return nil, err
			}

			// Allow Ingresses in whitelisted namespaces.
			for _, ns := range allowedNamespaces {
				if ingress.Namespace == ns {
					resp.Allowed = true
					resp.Result.Message = "%s namespace is whitelisted"
					return resp, nil
				}
			}

			return nil, fmt.Errorf("%s objects cannot be deployed to this cluster", kind)
		default:
			resp.Allowed = true
			return nil, nil
		}
	}
}

// DenyPublicLoadBalancers denies any non-internal public cloud load balancers
// (kind: Service of type: LoadBalancer) by looking for their "internal" load
// balancer annotations. This prevents accidentally exposing Services to the
// Internet for Kubernetes clusters designed to be internal-facing only.
//
// The required annotations are documented at
// https://kubernetes.io/docs/concepts/services-networking/#internal-load-balancer
//
// Services with a .spec.type other than LoadBalancer will NOT be rejected by this handler.
func DenyPublicLoadBalancers(allowedNamespaces []string, provider CloudProvider) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind
		resp := newDefaultDenyResponse()

		service := core.Service{}
		deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
		if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &service); err != nil {
			return nil, err
		}

		if service.Spec.Type != "LoadBalancer" {
			resp.Allowed = true
			resp.Result.Message = fmt.Sprintf(
				"DenyPublicLoadBalancers received a non-LoadBalancer type (%s)",
				service.Spec.Type,
			)
			return resp, nil
		}

		// Don't deny Services in whitelisted namespaces
		for _, ns := range allowedNamespaces {
			if service.Namespace == ns {
				// this namespace is whitelisted
			}
		}

		expectedAnnotations, ok := ilbAnnotations[provider]
		if !ok {
			return nil, fmt.Errorf("cannot validate the internal load balancer annotation for the given provider (%q)", provider)
		}

		if _, ok := ensureHasAnnotations(expectedAnnotations, service.ObjectMeta.Annotations); !ok {
			// does not have annotations; print missing
			return nil, fmt.Errorf("%s objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster", kind)
		}

		return resp, nil
	}
}

// ensureHasAnnotations checks whether the provided ObjectMeta has the required
// annotations. It returns both a map of missing annotations, and a boolean
// value if the meta had all of the provided annotations.
//
// The required annotations are case-sensitive; an empty string for the map
// value will match on key (only) and thus allow any value.
func ensureHasAnnotations(required map[string]string, annotations map[string]string) (map[string]string, bool) {

	return nil, false
}

// func DenyContainersWithMutableTags(allowedNamespaces []string, allowedTags []string) AdmitFunc {
// 	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
// 		kind := admissionReview.Request.Kind.Kind
// 		resp := newDefaultDenyResponse()
//
//		TODO(matt): Range over Containers in a Pod spec, parse image URL and inspect tags.
//
// 		return resp, nil
// 	}
// }

// func EnforcePodAnnotations(allowedNamespaces []string, requiredAnnotations map[string]string) AdmitFunc {
// 	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
// 		kind := admissionReview.Request.Kind.Kind
// 		resp := newDefaultDenyResponse()
//
//		TODO(matt): enforce annotations on a Pod
//
// 		return resp, nil
// 	}
// }
