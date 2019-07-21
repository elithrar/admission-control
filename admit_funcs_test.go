package admissioncontrol

import (
	"testing"

	admission "k8s.io/api/admission/v1beta1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type objectTest struct {
	testName        string
	cloudProvider   CloudProvider
	kind            meta.GroupVersionKind
	rawObject       []byte
	expectedMessage string
	shouldAllow     bool
}

func TestDenyIngress(t *testing.T) {
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
	}

	for _, tt := range denyTests {
		t.Run(tt.testName, func(t *testing.T) {
			incomingReview := admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			}
			incomingReview.Request.Kind = tt.kind
			incomingReview.Request.Object.Raw = tt.rawObject

			resp, err := DenyIngresses(nil)(&incomingReview)
			if err != nil {
				if tt.expectedMessage != err.Error() {
					t.Fatalf("error message does not match: got %q - expected %q", err.Error(), tt.expectedMessage)
				}

				if tt.shouldAllow {
					t.Fatalf("incorrectly rejected admission for %s (kind: %v): %s", tt.testName, tt.kind, err.Error())
				}

				t.Logf("correctly rejected admission for %s (kind: %v): %s", tt.testName, tt.kind, err.Error())
				return
			}

			if resp.Allowed != tt.shouldAllow {
				t.Fatalf("incorrectly allowed admission for %s (kind: %v): %s", tt.testName, tt.kind, resp.String())
			}
		})
	}

}

// TestDenyPublicServices checks that the correct kind, type & annotation combinations are valid for the AdmitFunc.
func TestDenyPublicLoadBalancers(t *testing.T) {
	var expectedLBMessage = "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster"

	var denyTests = []struct {
		testName        string
		cloudProvider   CloudProvider
		kind            meta.GroupVersionKind
		rawObject       []byte
		expectedMessage string
		shouldAllow     bool
	}{
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
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"cloud.google.com/load-balancer-type": "Internal"}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "",
			shouldAllow:     true,
		},
		{
			testName:      "Allow Annotated Private Service (Azure)",
			cloudProvider: Azure,
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"cloud.google.com/load-balancer-type": "Internal"}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
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
			expectedMessage: "",
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
			expectedMessage: expectedLBMessage,
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
			expectedMessage: expectedLBMessage,
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

			resp, err := DenyPublicLoadBalancers(nil, tt.cloudProvider)(&incomingReview)
			if err != nil {
				if tt.expectedMessage != err.Error() {
					t.Fatalf("error message does not match: got %q - expected %q", err.Error(), tt.expectedMessage)
				}

				if tt.shouldAllow {
					t.Fatalf("incorrectly rejected admission for %s (kind: %v): %s", tt.testName, tt.kind, err.Error())
				}

				t.Logf("correctly rejected admission for %s (kind: %v): %s", tt.testName, tt.kind, err.Error())
				return
			}

			if resp.Allowed != tt.shouldAllow {
				t.Fatalf("incorrectly allowed admission for %s (kind: %v): %s", tt.testName, tt.kind, resp.String())
			}
		})
	}
}
