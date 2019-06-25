package admissioncontrol

import (
	"encoding/json"
	"fmt"

	admission "k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
)

const (
	// Docs: https://cloud.google.com/kubernetes-engine/docs/how-to/internal-load-balancing#overview
	ilbAnnotationKey = "cloud.google.com/load-balancer-type"
	ilbAnnotationVal = "Internal"
)

// DenyPublicServices rejects any Ingress objects, and rejects any Service
// objects of type LoadBalancer without a GCP Internal Load Balancer annotation.
func DenyPublicServices(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
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
