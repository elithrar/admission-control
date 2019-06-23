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
		annotations     map[string]string
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
			annotations:     nil,
			rawObject:       []byte(""),
			expectedMessage: "Ingress objects cannot be deployed to this cluster",
			shouldAllow:     false,
		},
		// TODO(silverlock): Fix the rawObject parts of these tests - need to determine how we can provide a raw k8s object.
		// Similar tests here: https://github.com/kubernetes/apimachinery/blob/961b39a1baa06f6c52bdd048a809b9f5b47f1337/pkg/test/apis_meta_v1_unstructed_unstructure_test.go#L451
		//
		// {
		// 	testName: "Reject Public Service",
		// 	kind: meta.GroupVersionKind{
		// 		Group:   "",
		// 		Kind:    "Service",
		// 		Version: "v1",
		// 	},
		// 	annotations:     nil,
		// 	rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","uid":"696a7987-96b4-11e9-a629-42010a80013b","creationTimestamp":"2019-06-24T19:15:21Z","annotations":{"kubectl.kubernetes.io/last-applied-configuration":{"apiVersion":"v1","kind":"Service","metadata":{"name":"hello-service","namespace":"default"},"spec":{"ports":[{"port":8000,"protocol":"TCP","targetPort":8080}],"selector":{"app":"hello-app"},"type":"LoadBalancer"}}}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"clusterIP":"10.4.22.127","type":"LoadBalancer","sessionAffinity":"None","externalTrafficPolicy":"Cluster"},"status":{"loadBalancer":{}}}`),
		// 	expectedMessage: "Service objects of type: LoadBalancer without an internal-only annotation cannot be deployed to this cluster",
		// 	shouldAllow:     false,
		// },
		// {
		// 	testName: "Allow Annotated Private Service",
		// 	kind: meta.GroupVersionKind{
		// 		Group:   "",
		// 		Kind:    "Service",
		// 		Version: "v1",
		// 	},
		// 	annotations:     nil,
		// 	rawObject:       []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"hello-service","namespace":"default","uid":"696a7987-96b4-11e9-a629-42010a80013b","creationTimestamp":"2019-06-24T19:15:21Z","annotations":{"kubectl.kubernetes.io/last-applied-configuration":{"apiVersion":"v1","kind":"Service","metadata":{"annotations":{"cloud.google.com/load-balancer-type":"Internal"},"name":"hello-service","namespace":"default"},"spec":{"ports":[{"port":8000,"protocol":"TCP","targetPort":8080}],"selector":{"app":"hello-app"},"type":"LoadBalancer"}}}},"spec":{"ports":[{"protocol":"TCP","port":8000,"targetPort":8080,"nodePort":31433}],"selector":{"app":"hello-app"},"clusterIP":"10.4.22.127","type":"LoadBalancer","sessionAffinity":"None","externalTrafficPolicy":"Cluster"},"status":{"loadBalancer":{}}}`),
		// 	expectedMessage: "",
		// 	shouldAllow:     true,
		// },
		{
			testName: "Allow Pods",
			kind: meta.GroupVersionKind{
				Group:   "",
				Kind:    "Pod",
				Version: "v1",
			},
			annotations:     nil,
			rawObject:       []byte(""),
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
			annotations:     nil,
			rawObject:       []byte(""),
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
