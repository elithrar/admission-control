package admissioncontrol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	admission "k8s.io/api/admission/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

var (
	testErrAdmissionMismatch = "admission mismatch (kind: %v): got allowed=%t - wanted allowed=%t)"
	testErrMessageMismatch   = "error message does not match: got %q - expected %q"
)

type objectTest struct {
	testName            string
	admitFunc           AdmitFunc
	cloudProvider       CloudProvider
	requiredAnnotations map[string]func(string) bool
	kind                meta.GroupVersionKind
	object              interface{}
	rawObject           []byte
	ignoredNamespaces   []string
	expectedMessage     string
	shouldAllow         bool
}

func newTestAdmissionRequest(kind meta.GroupVersionKind, object []byte, expected bool) *admission.AdmissionReview {
	ar := &admission.AdmissionReview{
		Request: &admission.AdmissionRequest{
			Kind: kind,
			Object: runtime.RawExtension{
				Raw: object,
			},
		},
		Response: &admission.AdmissionResponse{},
	}

	return ar
}

// TestDenyIngress validates that the DenyIngress AdmitFunc correctly rejects
// admission of Ingress objects to a cluster.
func TestDenyIngress(t *testing.T) {
	t.Parallel()

	var deniedIngressError = "Ingress objects cannot be deployed to this cluster"
	var denyTests = []objectTest{
		{
			testName: "Reject Ingress (<= v1.13)",
			kind: meta.GroupVersionKind{
				Group:   "extensions",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:       []byte(`{"kind":"Ingress","apiVersion":"v1beta1","group":"extensions","metadata":{"name":"hello-ingress","namespace":"default","annotations":{}},"spec":{"rules":[]}}`),
			expectedMessage: deniedIngressError,
			shouldAllow:     false,
		},
		{
			testName: "Reject Ingress (>= v1.14)",
			kind: meta.GroupVersionKind{
				Group:   "networking.k8s.io",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:       []byte(`{"kind":"Ingress","apiVersion":"v1beta1","group":"networking.k8s.io","metadata":{"name":"hello-ingress","namespace":"default","annotations":{}},"spec":{"rules":[]}}`),
			expectedMessage: deniedIngressError,
			shouldAllow:     false,
		},
		{
			testName: "Allow admission to a whitelisted namespace",
			kind: meta.GroupVersionKind{
				Group:   "extensions",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:         []byte(`{"kind":"Ingress","apiVersion":"v1beta1","group":"extensions","metadata":{"name":"hello-ingress","namespace":"istio-system","annotations":{}},"spec":{"rules":[]}}`),
			ignoredNamespaces: []string{"istio-system"},
			expectedMessage:   "",
			shouldAllow:       true,
		},
		{
			testName: "Reject Ingress in incorrectly whitelisted namespace (case-sensitive)",
			kind: meta.GroupVersionKind{
				Group:   "extensions",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:         []byte(`{"kind":"Ingress","apiVersion":"v1beta1","group":"extensions","metadata":{"name":"hello-ingress","namespace":"UPPER-CASE","annotations":{}},"spec":{"rules":[]}}`),
			ignoredNamespaces: []string{"upper-case"},
			expectedMessage:   deniedIngressError,
			shouldAllow:       false,
		},
		{
			testName: "Don't reject Services",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster",
			shouldAllow:     true,
		},
		{
			testName: "Don't reject Pods",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			rawObject:       nil,
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Don't reject Deployments",
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "Deployment",
				Version: "v1",
			},
			rawObject:       nil,
			expectedMessage: "",
			shouldAllow:     true,
		},
	}

	for _, tt := range denyTests {
		t.Run(tt.testName, func(t *testing.T) {
			incomingReview := admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			}
			incomingReview.Request.Kind = tt.kind
			incomingReview.Request.Object.Raw = tt.rawObject

			resp, err := DenyIngresses(tt.ignoredNamespaces)(&incomingReview)
			if err != nil {
				if tt.expectedMessage != err.Error() {
					t.Fatalf(testErrMessageMismatch, err.Error(), tt.expectedMessage)
				}

				if tt.shouldAllow {
					t.Fatalf("incorrectly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				}

				t.Logf("correctly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				return
			}

			if resp.Allowed != tt.shouldAllow {
				t.Fatalf(testErrAdmissionMismatch, tt.kind, resp.Allowed, tt.shouldAllow)
			}
		})
	}

}

// TestDenyPublicServices checks that the DenyPublicServices AdmitFunc correctly
// rejects non-internal load balancer admission to a cluster.
func TestDenyPublicLoadBalancers(t *testing.T) {
	t.Parallel()

	var missingLBAnnotationsMessage = "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster"

	var denyTests = []objectTest{
		{
			testName:      "Reject Public Service",
			cloudProvider: GCP,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster",
			shouldAllow:     false,
		},
		{
			testName:      "Allow Annotated Private Service (GCP)",
			cloudProvider: GCP,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"cloud.google.com/load-balancer-type":"Internal"}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName:      "Allow Public Service in Whitelisted Namespace",
			cloudProvider: GCP,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:         []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"web-services","annotations":{}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			ignoredNamespaces: []string{"web-services"},
			expectedMessage:   "",
			shouldAllow:       true,
		},
		{
			testName:      "Reject public Service in incorrectly whitelisted namespace (case-sensitive)",
			cloudProvider: GCP,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:         []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"WEB-SERVICES","annotations":{}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			ignoredNamespaces: []string{"web-services"},
			expectedMessage:   missingLBAnnotationsMessage,
			shouldAllow:       false,
		},
		{
			testName:      "Allow Annotated Private Service (Azure)",
			cloudProvider: Azure,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"service.beta.kubernetes.io/azure-load-balancer-internal":"true"}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName:      "Allow Annotated Private Service (AWS)",
			cloudProvider: AWS,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"service.beta.kubernetes.io/aws-load-balancer-internal":"0.0.0.0/0"}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: missingLBAnnotationsMessage,
			shouldAllow:     true,
		},
		{
			testName:      "Reject Incorrectly Annotated Private Service (no annotation)",
			cloudProvider: GCP,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"cloud.google.com/load-balancer-type": ""}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: missingLBAnnotationsMessage,
			shouldAllow:     false,
		},
		{
			testName:      "Reject Incorrectly Annotated Private Service (missing annotation val)",
			cloudProvider: GCP,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"cloud.google.com/load-balancer-type": ""}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: missingLBAnnotationsMessage,
			shouldAllow:     false,
		},
		{
			testName:      "Reject Incorrectly Annotated Private Service (Azure provider, AWS annotation)",
			cloudProvider: Azure,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"service.beta.kubernetes.io/aws-load-balancer-internal": "0.0.0.0/0"}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster",
			shouldAllow:     false,
		},
		{
			testName: "Don't reject Ingress (<= v1.13)",
			kind: meta.GroupVersionKind{
				Group:   "extensions",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:       []byte(`{"kind":"Ingress","apiVersion":"v1beta1","group":"extensions","metadata":{"name":"hello-ingress","namespace":"default","annotations":{}},"spec":{"rules":[]}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{

			testName: "Don't reject Ingress (>= v1.14)",
			kind: meta.GroupVersionKind{
				Group:   "networking.k8s.io",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:       []byte(`{"kind":"Ingress","apiVersion":"v1beta1","group":"networking.k8s.io","metadata":{"name":"hello-ingress","namespace":"default","annotations":{}},"spec":{"rules":[]}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Don't reject Pods",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			rawObject:       nil,
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Don't reject Deployments",
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "Deployment",
				Version: "v1",
			},
			rawObject:       nil,
			expectedMessage: "",
			shouldAllow:     true,
		},
	}

	for _, tt := range denyTests {
		t.Run(tt.testName, func(t *testing.T) {
			incomingReview := admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			}
			incomingReview.Request.Kind = tt.kind
			incomingReview.Request.Object.Raw = tt.rawObject

			resp, err := DenyPublicLoadBalancers(tt.ignoredNamespaces, tt.cloudProvider)(&incomingReview)
			if err != nil {
				if tt.expectedMessage != err.Error() {
					t.Fatalf(testErrMessageMismatch, err.Error(), tt.expectedMessage)
				}

				if tt.shouldAllow {
					t.Fatalf("incorrectly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				}

				t.Logf("correctly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				return
			}

			if resp.Allowed != tt.shouldAllow {
				t.Fatalf(testErrAdmissionMismatch, tt.kind, resp.Allowed, tt.shouldAllow)
			}
		})
	}
}

func TestAddAutoscalerAnnotation(t *testing.T) {
	t.Parallel()

	var autoscalerTest = []objectTest{
		{
			testName: "Add annotation if it does not exist",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			rawObject:         []byte(`{"kind":"Pod","apiVersion":"v1","group":"","metadata":{"name":"hello-app","namespace":"default","annotations":{"questionable.services/hostname":"hello-app.questionable.services","buildVersion":"v1.0.2"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}`),
			expectedMessage:   "",
			shouldAllow:       true,
			ignoredNamespaces: []string{},
			object:            "[{\"op\":\"add\",\"path\":\"/metadata/annotations\",\"value\":{\"cluster-autoscaler.kubernetes.io/safe-to-evict\":\"true\"}}]",
		},
		{
			testName: "skip adding annotation if it does exist",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			rawObject:         []byte(`{"kind":"Pod","apiVersion":"v1","group":"","metadata":{"name":"hello-app","namespace":"default","annotations":{"questionable.services/hostname":"hello-app.questionable.services","buildVersion":"v1.0.2", "cluster-autoscaler.kubernetes.io/safe-to-evict":"true"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}`),
			expectedMessage:   "",
			shouldAllow:       true,
			ignoredNamespaces: []string{},
			object:            nil,
		},
		{
			testName: "skip if object is not a pod",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:         []byte(`{"kind":"Service","apiVersion":"v1","group":"","metadata":{"name":"hello-app","namespace":"default","annotations":{"questionable.services/hostname":"hello-app.questionable.services","buildVersion":"v1.0.2", "cluster-autoscaler.kubernetes.io/safe-to-evict":"true"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}`),
			expectedMessage:   "object was not a pod, Service",
			shouldAllow:       true,
			ignoredNamespaces: []string{},
			object:            nil,
		},
	}

	for _, tt := range autoscalerTest {
		t.Run(tt.testName, func(t *testing.T) {

			incomingReview := admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			}
			incomingReview.Request.Kind = tt.kind
			incomingReview.Request.Object.Raw = tt.rawObject

			resp, err := AddAutoscalerAnnotation(tt.ignoredNamespaces)(&incomingReview)
			if err != nil {
				if tt.shouldAllow {
					t.Fatalf("incorrectly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				}
			}

			if resp == nil {
				t.Fatalf("response should not be nil")
			}

			if tt.expectedMessage != resp.Result.Message {
				t.Fatalf("response message does not match expected, %s", resp.Result.Message)
			}

			if tt.object != nil && resp != nil {
				if string(resp.Patch) != tt.object {
					t.Fatalf("Did not get the expected patch, got %s", resp.Patch)
				}
			}

		})
	}

}

func TestEnforcePodAnnotations(t *testing.T) {
	t.Parallel()

	var denyTests = []objectTest{
		{
			testName: "Allow Pod with required annotations",
			requiredAnnotations: map[string]func(string) bool{
				"questionable.services/hostname": func(s string) bool { return true },
				"buildVersion":                   func(s string) bool { return strings.HasPrefix(s, "v") },
			},
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Pod","apiVersion":"v1","group":"","metadata":{"name":"hello-app","namespace":"default","annotations":{"questionable.services/hostname":"hello-app.questionable.services","buildVersion":"v1.0.2"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Reject Pod with missing annotations",
			requiredAnnotations: map[string]func(string) bool{
				"questionable.services/hostname": func(s string) bool { return true },
			},
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			// missing the "hostname" annotation
			rawObject:       []byte(`{"kind":"Pod","apiVersion":"v1","group":"","metadata":{"name":"hello-app","namespace":"default","annotations":{"buildVersion":"v1.0.2"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}`),
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[questionable.services/hostname:key was not found]"),
			shouldAllow:     false,
		},
		{
			testName: "Reject Pod with invalid annotation value",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			// buildVersion is missing the "v" in the version number
			rawObject:       []byte(`{"kind":"Pod","apiVersion":"v1","group":"","metadata":{"name":"hello-app","namespace":"default","annotations":{"buildVersion":"1.0.2"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}`),
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[buildVersion:value did not match]"),
			shouldAllow:     false,
		},
		{
			testName: "Allow admission to a whitelisted namespace",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			ignoredNamespaces: []string{"istio-system"},
			object: &corev1.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Namespace: "istio-system"},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}},
			},
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Unhandled Kinds (Service) are correctly rejected",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: fmt.Sprintf("%s %s", unsupportedKindError, "Service"),
			shouldAllow:     false,
		},
		{
			testName: "Allow correctly annotated Pods in a Deployment",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "Deployment",
				Version: "v1",
			},
			object: &appsv1.Deployment{
				TypeMeta:   meta.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
				ObjectMeta: meta.ObjectMeta{Namespace: "default"},
				Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{"buildVersion": "v1.0.0"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}}}},
			},
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Reject unannotated Pods in a Deployment",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "Deployment",
				Version: "v1",
			},
			object: &appsv1.Deployment{
				TypeMeta:   meta.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
				ObjectMeta: meta.ObjectMeta{Namespace: "default"},
				Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: meta.ObjectMeta{}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}}}},
			},
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[buildVersion:key was not found]"),
			shouldAllow:     false,
		},
		{
			testName: "Allow correctly annotated Pods in a DaemonSet",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "DaemonSet",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"DaemonSet","apiVersion":"v1","group":"apps","metadata":{"name":"hello-daemonset","namespace":"default","annotations":{}},"spec":{"template":{"metadata":{"annotations":{"buildVersion":"v1.0.0"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Reject unannotated Pods in a DaemonSet",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "DaemonSet",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"DaemonSet","apiVersion":"v1","group":"apps","metadata":{"name":"hello-daemonset","namespace":"default","annotations":{}},"spec":{"template":{"metadata":{"annotations":{}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}}}`),
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[buildVersion:key was not found]"),
			shouldAllow:     false,
		},
		{
			testName: "Allow correctly annotated Pods in a StatefulSet",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "StatefulSet",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"StatefulSet","apiVersion":"v1","group":"apps","metadata":{"name":"hello-statefulset","namespace":"default","annotations":{}},"spec":{"template":{"metadata":{"annotations":{"buildVersion":"v1.0.0"}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Reject unannotated Pods in a StatefulSet",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "StatefulSet",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"StatefulSet","apiVersion":"v1","group":"apps","metadata":{"name":"hello-statefulset","namespace":"default","annotations":{}},"spec":{"template":{"metadata":{"annotations":{}},"spec":{"containers":[{"name":"nginx","image":"nginx:latest"}]}}}}`),
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[buildVersion:key was not found]"),
			shouldAllow:     false,
		},
		{
			testName: "Allow correctly annotated Pods in a Job",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "batch",
				Kind:    "Job",
				Version: "v1",
			},
			object: &batchv1.Job{
				TypeMeta:   meta.TypeMeta{Kind: "Job", APIVersion: "batch/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "", Namespace: "default"},
				Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{ObjectMeta: meta.ObjectMeta{Annotations: map[string]string{"buildVersion": "v1.0.0"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}}}},
			},
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName: "Reject unannotated Pods in a Job",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "batch",
				Kind:    "Job",
				Version: "v1",
			},
			object: &batchv1.Job{
				TypeMeta:   meta.TypeMeta{Kind: "Job", APIVersion: "batch/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "", Namespace: "default"},
				Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{ObjectMeta: meta.ObjectMeta{Name: ""}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}}}},
			},
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[buildVersion:key was not found]"),
			shouldAllow:     false,
		},
		{
			testName: "Reject cases where the outer object is annotated, but the PodTemplateSpec is not",
			requiredAnnotations: map[string]func(string) bool{
				"buildVersion": func(s string) bool { return strings.HasPrefix(s, "v") }},
			kind: meta.GroupVersionKind{
				Group:   "apps",
				Kind:    "Deployment",
				Version: "v1",
			},
			object: &appsv1.Deployment{
				TypeMeta: meta.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
				ObjectMeta: meta.ObjectMeta{Namespace: "default", Annotations: map[string]string{
					"buildVersion": "v1.0.0",
				}},
				Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: meta.ObjectMeta{Name: ""}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}}}},
			},
			expectedMessage: fmt.Sprintf("%s %s", podDeniedError, "map[buildVersion:key was not found]"),
			shouldAllow:     false,
		},
	}

	for _, tt := range denyTests {
		t.Run(tt.testName, func(t *testing.T) {
			incomingReview := admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			}

			incomingReview.Request.Kind = tt.kind

			if tt.rawObject == nil {
				serialized, err := json.Marshal(tt.object)
				if err != nil {
					t.Fatalf("could not marshal k8s API object: %v", err)
				}

				incomingReview.Request.Object.Raw = serialized
			} else {
				incomingReview.Request.Object.Raw = tt.rawObject
			}

			resp, err := EnforcePodAnnotations(tt.ignoredNamespaces, tt.requiredAnnotations)(&incomingReview)
			if err != nil {
				if tt.expectedMessage != err.Error() {
					t.Fatalf(testErrMessageMismatch, err.Error(), tt.expectedMessage)
				}

				if tt.shouldAllow {
					t.Fatalf("incorrectly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				}

				t.Logf("correctly rejected admission for Kind: %v: %s", tt.kind, err.Error())
				return
			}

			if resp.Allowed != tt.shouldAllow {
				t.Fatalf(testErrAdmissionMismatch, tt.kind, resp.Allowed, tt.shouldAllow)
			}
		})
	}

}
