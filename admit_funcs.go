package admissioncontrol

import (
	"encoding/json"
	"fmt"

	"golang.org/x/xerrors"

	admission "k8s.io/api/admission/v1beta1"
	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	podDeniedError       = "the submitted Pods are missing required annotations:"
	unsupportedKindError = "the submitted Kind is not supported by this admission handler:"
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

const clusterAutoScalerAnnotationKey = "cluster-autoscaler.kubernetes.io/safe-to-evict"

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// ilbAnnotations maps the annotation key:value pairs required to denote an
// internal-only load balancer on the supported cloud platforms.
//
// Docs: https://kubernetes.io/docs/concepts/services-networking/#internal-load-balancer
var ilbAnnotations = map[CloudProvider]map[string]string{
	GCP:       {"cloud.google.com/load-balancer-type": "Internal"},
	Azure:     {"service.beta.kubernetes.io/azure-load-balancer-internal": "true"},
	AWS:       {"service.beta.kubernetes.io/aws-load-balancer-internal": "0.0.0.0/0"},
	OpenStack: {"service.beta.kubernetes.io/openstack-internal-load-balancer": "true"},
}

// newDefaultDenyResponse returns an AdmissionResponse with a Result sub-object,
// and defaults to allowed = false.
func newDefaultDenyResponse() *admission.AdmissionResponse {
	return &admission.AdmissionResponse{
		Allowed: false,
		Result:  &metav1.Status{},
	}
}

// Add autoscaler annotation

func AddAutoscalerAnnotation(ignoredNamespaces []string) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind
		deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
		resp := newDefaultDenyResponse()

		var namespace string
		annotations := make(map[string]string)
		pod := &core.Pod{}

		switch kind {
		case "Pod":
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, pod); err != nil {
				return nil, err
			}
			namespace = pod.GetNamespace()
			annotations = pod.GetAnnotations()
		default:
			resp.Allowed = true
			resp.Result.Message = fmt.Sprintf("object was not a pod, %s", kind)
			return resp, nil

		}

		// Ignore objects in whitelisted namespaces.
		for _, ns := range ignoredNamespaces {
			if namespace == ns {
				resp.Allowed = true
				resp.Result.Message = fmt.Sprintf("allowing admission: %s namespace is whitelisted", namespace)
				return resp, nil
			}
		}

		// Check for auto scaler key
		_, ok := annotations[clusterAutoScalerAnnotationKey]
		if ok {
			resp.Allowed = true
			resp.Result.Message = fmt.Sprintf("object already has auto scaler annotation")
			return resp, nil
		}

		patch, err := GetPatch(pod)
		if err != nil {
			return nil, err
		}

		return &admission.AdmissionResponse{
			Allowed: true,
			Patch:   patch,
			Result: &metav1.Status{
				Message: "Updating annotations",
			},
			PatchType: func() *admission.PatchType {
				pt := admission.PatchTypeJSONPatch
				return &pt
			}(),
		}, nil

	}
}

func GetPatch(pod *core.Pod) ([]byte, error) {
	var patch []patchOperation
	patch = append(patch, updateAnnotation(pod.Annotations, map[string]string{clusterAutoScalerAnnotationKey: "true"})...)
	return json.Marshal(patch)
}

func updateAnnotation(target map[string]string, added map[string]string) (patch []patchOperation) {
	for key, value := range added {
		if target == nil || target[key] == "" {
			target = map[string]string{}
			patch = append(patch, patchOperation{
				Op:   "add",
				Path: "/metadata/annotations",
				Value: map[string]string{
					key: value,
				},
			})
		} else {
			patch = append(patch, patchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations/" + key,
				Value: value,
			})
		}
	}
	return patch
}

// DenyIngresses denies any kind: Ingress from being deployed to the cluster,
// except for any explicitly allowed namespaces (e.g. istio-system).
//
// Providing an empty/nil list of ignoredNamespaces will reject Ingress objects
// across all namespaces.
//
// Kinds other than Ingress will be allowed.
func DenyIngresses(ignoredNamespaces []string) AdmitFunc {
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

			// Ignore objects in whitelisted namespaces.
			for _, ns := range ignoredNamespaces {
				if ingress.Namespace == ns {
					resp.Allowed = true
					resp.Result.Message = fmt.Sprintf("allowing admission: %s namespace is whitelisted", ingress.Namespace)
					return resp, nil
				}
			}

			return nil, xerrors.Errorf("%s objects cannot be deployed to this cluster", kind)
		default:
			resp.Allowed = true
			return resp, nil
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
// Services with a .spec.type other than LoadBalancer will NOT be rejected by
// this handler.
//
// Providing an empty/nil list of ignoredNamespaces will reject LoadBalancers
// across all namespaces.
func DenyPublicLoadBalancers(ignoredNamespaces []string, provider CloudProvider) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind
		resp := newDefaultDenyResponse()

		service := core.Service{}
		deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
		if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &service); err != nil {
			return nil, err
		}

		if kind != "Service" || service.Spec.Type != "LoadBalancer" {
			resp.Allowed = true
			resp.Result.Message = fmt.Sprintf(
				"DenyPublicLoadBalancers received a non-LoadBalancer type (%s)",
				service.Spec.Type,
			)
			return resp, nil
		}

		// Ignore objects in whitelisted namespaces.
		for _, ns := range ignoredNamespaces {
			if service.Namespace == ns {
				resp.Allowed = true
				resp.Result.Message = fmt.Sprintf("allowing admission: %s namespace is whitelisted", service.Namespace)
				return resp, nil
			}
		}

		expectedAnnotations, ok := ilbAnnotations[provider]
		if !ok {
			return resp, xerrors.Errorf("internal load balancer annotations for the given provider (%q) are not supported", provider)
		}

		// TODO(matt): If we're missing any annotations, provide them in the AdmissionResponse so
		// the user can correct them.
		if _, ok := ensureHasAnnotations(expectedAnnotations, service.ObjectMeta.Annotations); !ok {
			return resp, xerrors.Errorf("%s objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster", kind)
		}

		// No missing or invalid annotations; allow admission
		resp.Allowed = true
		return resp, nil
	}
}

// EnforcePodAnnotations ensures that Pods have the required annotations by
// looking for a strict (case-sensitive) key-match, and then running the
// matchFunc (a func(string) bool) over the value.
//
// This allows the caller to perform flexible matching - checking for valid DNS
// names or a list of accepted values - rather than having to iterate over all
// possible values, which may not be possible.
//
// EnforcePodAnnotations can inspect Pods, Deployments, StatefulSets, DaemonSets &
// Jobs.
//
// Unknown object kinds are rejected. You can create multiple versions of
// this AdmitFunc for a given ValidatingAdmissionWebhook configuration if you
// wish to apply different configurations per kind or namespace.
func EnforcePodAnnotations(ignoredNamespaces []string, requiredAnnotations map[string]func(string) bool) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		kind := admissionReview.Request.Kind.Kind
		resp := newDefaultDenyResponse()

		deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()

		// We handle all built-in Kinds that include a PodTemplateSpec, as described here:
		// https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.15/#pod-v1-core
		var namespace string
		annotations := make(map[string]string)
		// Extract the necessary metadata from our known Kinds
		switch kind {
		case "Pod":
			pod := core.Pod{}
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &pod); err != nil {
				return nil, err
			}

			namespace = pod.GetNamespace()
			annotations = pod.GetAnnotations()
		case "Deployment":
			deployment := apps.Deployment{}
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &deployment); err != nil {
				return nil, err
			}

			deployment.GetNamespace()
			annotations = deployment.Spec.Template.GetAnnotations()
		case "StatefulSet":
			statefulset := apps.StatefulSet{}
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &statefulset); err != nil {
				return nil, err
			}

			namespace = statefulset.GetNamespace()
			annotations = statefulset.Spec.Template.GetAnnotations()
		case "DaemonSet":
			daemonset := apps.DaemonSet{}
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &daemonset); err != nil {
				return nil, err
			}

			namespace = daemonset.GetNamespace()
			annotations = daemonset.Spec.Template.GetAnnotations()
		case "Job":
			job := batch.Job{}
			if _, _, err := deserializer.Decode(admissionReview.Request.Object.Raw, nil, &job); err != nil {
				return nil, err
			}

			namespace = job.Spec.Template.GetNamespace()
			annotations = job.Spec.Template.GetAnnotations()
		default:
			// TODO(matt): except for whitelisted namespaces
			return nil, xerrors.Errorf("the submitted Kind is not supported by this admission handler: %s", kind)
		}

		// Ignore objects in whitelisted namespaces.
		for _, ns := range ignoredNamespaces {
			if namespace == ns {
				resp.Allowed = true
				resp.Result.Message = fmt.Sprintf("allowing admission: %s namespace is whitelisted", namespace)
				return resp, nil
			}
		}

		missing := make(map[string]string)
		// We check whether the (strictly matched) annotation key exists, and then run
		// our user-provided matchFunc against it. If we're missing any keys, or the
		// value for a key does not match, admission is rejected.
		for requiredKey, matchFunc := range requiredAnnotations {
			if matchFunc == nil {
				return resp, xerrors.Errorf("cannot validate annotations (%s) with a nil matchFunc", requiredKey)
			}

			if existingVal, ok := annotations[requiredKey]; !ok {
				// Key does not exist; add it to the missing annotations list
				missing[requiredKey] = "key was not found"
			} else {
				if matched := matchFunc(existingVal); !matched {
					missing[requiredKey] = "value did not match"
				}
				// Key exists & matchFunc returned OK.
			}
		}

		if len(missing) > 0 {
			return resp, xerrors.Errorf("%s %v", podDeniedError, missing)
		}

		// No missing or invalid annotations; allow admission
		resp.Allowed = true
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
	missing := make(map[string]string)
	for requiredKey, requiredVal := range required {
		if existingVal, ok := annotations[requiredKey]; !ok {
			// Missing a required annotation; add it to the list
			missing[requiredKey] = requiredVal
		} else {
			// The key exists; does the value match?
			if existingVal != requiredVal {
				missing[requiredKey] = requiredVal
			}
		}
	}

	// If we have any missing annotations, report them to the caller so the user
	// can take action.
	if len(missing) > 0 {
		return missing, false
	}

	return nil, true
}
