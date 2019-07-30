package admissioncontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	admission "k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestAdmitFunc(allowed bool, returnError bool) AdmitFunc {
	return func(admissionReview *admission.AdmissionReview) (*admission.AdmissionResponse, error) {
		ar := &admission.AdmissionResponse{
			Allowed: allowed,
			Result:  &metav1.Status{},
		}

		if !allowed {
			return ar, errors.New("admission not allowed")
		}

		return ar, nil
	}
}

func TestAdmissionHandler(t *testing.T) {
	var handlerTests = []struct {
		testName       string
		admitFunc      AdmitFunc
		incomingReview *admission.AdmissionReview
		shouldPass     bool
	}{
		{
			testName:  "Pass-through AdmitFunc returns HTTP 200 & allows admission",
			admitFunc: newTestAdmitFunc(true, false),
			incomingReview: &admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			},
			shouldPass: true,
		},
		{
			testName:  "AdmitFunc returns HTTP 200 & denies admission",
			admitFunc: newTestAdmitFunc(false, true),
			incomingReview: &admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			},
			shouldPass: false,
		},
		{
			testName:       "Reject a nil/empty AdmissionReview",
			admitFunc:      newTestAdmitFunc(false, true),
			incomingReview: nil,
			shouldPass:     false,
		},
		{
			testName:  "Reject a malformed AdmissionReview (no Kind)",
			admitFunc: newTestAdmitFunc(false, true),
			incomingReview: &admission.AdmissionReview{
				Request: &admission.AdmissionRequest{},
			},
			shouldPass: false,
		},
	}

	for _, tt := range handlerTests {
		t.Run(tt.testName, func(t *testing.T) {
			handler := &AdmissionHandler{
				AdmitFunc: tt.admitFunc,
				Logger:    &noopLogger{},
			}

			buf := &bytes.Buffer{}
			err := json.NewEncoder(buf).Encode(&tt.incomingReview)
			if err != nil {
				t.Fatalf("error marshalling incomingReview: %v", err)
			}

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPost,
				"/",
				buf,
			)

			handler.ServeHTTP(rr, req)

			// Did get a non-nil response, and did it return a valid AdmissionReview
			// object?
			if rr.Body.Len() == 0 {
				t.Fatalf("received an empty response body")
			}

			review := &admission.AdmissionReview{}
			if err := json.Unmarshal(rr.Body.Bytes(), review); err != nil {
				t.Fatalf("couldn't marshal the review response: %v", err)
			}

			// Was the admission request correctly allowed?
			if allowed := review.Response.Allowed; allowed != tt.shouldPass {
				t.Fatalf("invalid review response: got allowed: %t (want %t)", allowed, tt.shouldPass)
			}
		})
	}

}
