package admissioncontrol

import (
	"testing"

	admission "k8s.io/api/admission/v1beta1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDenyPublicServices(t *testing.T) {
	var denyTests = []struct {
		testName        string
		kind            meta.GroupVersionKind
		rawObject       []byte
		expectedMessage string
		shouldAllow     bool
	}{
		{
			testName: "Reject Ingress",
			kind: meta.GroupVersionKind{
				Group:   "extensions",
				Kind:    "Ingress",
				Version: "v1beta1",
			},
			rawObject:       nil,
			expectedMessage: "Ingress objects cannot be deployed to this cluster",
			shouldAllow:     false,
		},
		// TODO(silverlock): Fix the rawObject parts of these tests - need to determine how we can provide a raw k8s object.
		// Similar tests here: https://github.com/kubernetes/apimachinery/blob/961b39a1baa06f6c52bdd048a809b9f5b47f1337/pkg/test/apis_meta_v1_unstructed_unstructure_test.go#L451
		//
		{
			testName: "Reject Public Service",
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
			testName: "Allow Annotated Private Service",
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
			testName: "Reject Incorrectly Annotated Private Service",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Service",
				Version: "v1",
			},
			rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","annotations":{"cloud.google.com/load-balancer-type": ""}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"type":"LoadBalancer","externalTrafficPolicy":"Cluster"}}`),
			expectedMessage: "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster",
			shouldAllow:     false,
		},
		{
			testName: "Allow Pods",
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
			testName: "Allow Deployments",
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

			resp, err := DenyPublicServices(&incomingReview)
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
