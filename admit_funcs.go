package admissioncontrol

import (
	"encoding/json"
	"errors"
	"fmt"

	admission "k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

// CloudProvider represents supported cloud platforms for provider-specific
// configuration.
type CloudProvider int

const (
	GCP CloudProvider = iota
	Azure
	AWS
	OpenStack
)

// Docs: https://kubernetes.io/docs/concepts/services-networking/#internal-load-balancer
var ilbAnnotations = map[CloudProvider]map[string]string{
	GCP:       {"cloud.google.com/load-balancer-type": "Internal"},
	Azure:     {"service.beta.kubernetes.io/azure-load-balancer-internal": "true"},
	AWS:       {"service.beta.kubernetes.io/aws-load-balancer-internal": "0.0.0.0/0"},
	OpenStack: {"service.beta.kubernetes.io/openstack-internal-load-balancer": "true"},
}

// DenyIngresses denies any kind: Ingress from being deployed to the cluster,
// except for whitelisted namespaces (e.g. istio-system).
//
// Providing an empty/nil list of allowedNamespaces will reject Ingress objects
// across all namespaces. Kinds other than Ingress will be allowed.
func DenyIngresses(allowedNamespaces []string) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind // Base Kind - e.g. "Service" as opposed to "v1/Service"
		resp := &admission.AdmissionResponse{
			Allowed: false, // Default deny
		}

		switch kind {
		case "Ingress":
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
// Services of types other than LoadBalancer will not be rejected by this handler.
func DenyPublicLoadBalancers(allowedNamespaces []string, provider CloudProvider) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind
		resp := &admission.AdmissionResponse{
			Allowed: false,
		}

		service := core.Service{}
		deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
		if _, _, err := deserializer.Decode(raw, nil, &service); err != nil {
			return nil, err
		}

		switch provider {
		case GCP:
			// deserialize request into concrete Service type
			// Inspect ObjectMeta.Annotations for matching key+val
			// Reject if not present
			if _, ok := ensureHasAnnotations(ilbAnnotations[GCP], meta.ObjectMeta{}); !ok {
				// does not have annotations; print missing
				return nil, fmt.Errorf("%s objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster", kind)
			}
		case Azure:
			//
		case AWS:
			//
		case OpenStack:
			//
		default:
			// default deny
			return nil, fmt.Errorf("cannot validate the internal load balancer annotation for the given provider (%q)", provider)
		}

		return resp, nil
	}
}

// DenyPublicServices rejects any Ingress objects, and rejects any Service
// objects of type LoadBalancer without a GCP Internal Load Balancer annotation.
func DenyPublicServices(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
	if admissionReview == nil || admissionReview.Request == nil {
		return nil, errors.New("received invalid AdmissionReview")
	}

	kind := admissionReview.Request.Kind.Kind // Base Kind - e.g. "Service" as opposed to "v1/Service"
	resp := &admission.AdmissionResponse{
		Allowed: false, // Default deny
	}

	switch kind {
	case "Ingress":
		return nil, fmt.Errorf("%s objects cannot be deployed to this cluster", kind)
	case "Service":
		service := core.Service{}
		if err := json.Unmarshal(admissionReview.Request.Object.Raw, &service); err != nil {
			return nil, err
		}

		if service.Spec.Type == "LoadBalancer" {
			if val, ok := service.ObjectMeta.Annotations[ilbAnnotationKey]; ok {
				if val == ilbAnnotationVal {
					resp.Allowed = true
					return resp, nil
				}

				// Not allowed when annotation value doesn't match.
				resp.Allowed = false
			}

			return nil, fmt.Errorf("%s objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster", kind)
		}

		fallthrough
	default:
		resp.Allowed = true
	}

	return resp, nil
}

// ensureHasAnnotations checks whether the provided ObjectMeta has the required
// annotations. It returns both a map of missing annotations, and a boolean
// value if the meta had all of the provided annotations.
//
// The required annotations are case-sensitive; an empty string for the map
// value will match on key (only) and thus allow any value.
func ensureHasAnnotations(required map[string]string, objectMeta meta.ObjectMeta) (map[string]string, bool) {

	return nil, false
}

// DenyPodWithoutAnnotations rejects Pods without the provided map of
// annotations (keys, values). The annotations must match exactly
// (case-sensitive).
// func DenyPodWithoutAnnotations(requiredAnnotations map[string]string) func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
// 	admitFunc := func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
// 		allowed := false
//
// 		kind := admissionReview.Request.Kind.Kind
// 		// name := admissionReview.Request.Name
// 		resp := &admission.AdmissionResponse{
// 			Allowed: allowed,
// 		}
//
// 		if kind == "Pod" {
// 			pod := core.Pod{}
// 			if err := json.Unmarshal(admissionReview.Request.Object.Raw, &pod); err != nil {
// 				return nil, err
// 			}
//
// 			annotations := pod.ObjectMeta.Annotations
// 			missing := map[string]string{}
// 			for requiredKey, requiredVal := range requiredAnnotations {
// 				if meta.HasAnnotation(pod.ObjectMeta, requiredKey) {
// 					if annotations[requiredKey] != requiredVal {
// 						resp.Allowed = false
// 						// Required value does not match
// 						// Add to "missing" list to report back on
// 					}
// 					// Has key & matching value
// 				}
// 				// does not have key at all
// 				// add to "missing" list to report back on
// 			}
//
// 			if len(missing) == 0 {
// 				resp.Allowed = true
// 			}
//
// 			// for requiredKey, requiredVal := range requiredAnnotations {
// 			// 	if actualVal, ok := annotations[requiredKey]; ok {
// 			// 		if actualVal != requiredVal {
// 			// 			return nil, fmt.Errorf("the submitted %s (name: %s) is missing required annotations: %#v", kind, name, requiredAnnotations)
// 			// 		}
// 			// 	} else {
// 			// 		return nil, fmt.Errorf("the submitted %s (name: %s) is missing required annotations: %#v", kind, name, requiredAnnotations)
// 			// 	}
// 			// }
// 		} else {
// 			resp.Allowed = true
// 		}
//
// 		return resp, nil
// 	}
//
// 	return admitFunc
// }
//
