package admissioncontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	admission "k8s.io/api/admission/v1beta1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	log "github.com/go-kit/kit/log"
)

// AdmitFunc checks whether an admission request is valid, and should return an
// admission response that sets AdmissionResponse.Allowed to true or false as
// needed.
//
// Users wishing to build their own admission handlers should satisfy the
// AdmitFunc type, and pass it to an AdmissionHandler for serving over HTTP.
//
// Note: this mirrors the type in k8s source:
// https://github.com/kubernetes/kubernetes/blob/v1.13.0/test/images/webhook/main.go#L43-L44
type AdmitFunc func(reviewRequest *admission.AdmissionReview) (*admission.AdmissionResponse, error)

// AdmissionHandler represents the configuration & associated endpoint for an
// k8s ValidatingAdmissionController (or MutatingAdmissionController) webhook.
//
// Multiple instances can be created with distinct AdmitFuncs to handle
// different admission requirements.
type AdmissionHandler struct {
	// The AdmitFunc to invoke for this handler.
	AdmitFunc AdmitFunc
	// A kitlog.Logger compatible interface
	Logger log.Logger
	// LimitBytes limits the size of objects the webhook will handle.
	LimitBytes int64
	// deserializer supports deserializing k8s objects. It can be left null; the
	// ServeHTTP function will lazily instantiate a decoder instance.
	deserializer runtime.Decoder
}

func (ah *AdmissionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ah.deserializer == nil {
		runtimeScheme := runtime.NewScheme()
		ah.deserializer = serializer.NewCodecFactory(runtimeScheme).UniversalDeserializer()
	}

	if ah.LimitBytes <= 0 {
		ah.LimitBytes = 1024 * 1024 * 1024 // 1MB
	}

	outgoingReview := &admission.AdmissionReview{
		Response: &admission.AdmissionResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := ah.handleAdmissionRequest(w, r); err != nil {
		outgoingReview.Response.Allowed = false
		outgoingReview.Response.Result = &meta.Status{
			Message: err.Error(),
		}

		admissionErr, ok := err.(AdmissionError)
		if ok {
			ah.Logger.Log(
				"msg", admissionErr.Message,
				"debug", admissionErr.Debug,
			)
			outgoingReview.Response.Allowed = admissionErr.Allowed
		}

		res, err := json.Marshal(outgoingReview)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			ah.Logger.Log(
				"err", err.Error(),
				"msg", "failed to marshal review response",
			)

			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(res)
	}
}

// AdmissionError represents an error (rejection, serialization error, etc) from
// an AdmissionHandler endpoint/handler.
type AdmissionError struct {
	Allowed bool
	Message string
	Debug   string
}

func (e AdmissionError) Error() string {
	return fmt.Sprintf("admission error: %s (allowed: %t)", e.Message, e.Allowed)
}

func (ah *AdmissionHandler) handleAdmissionRequest(w http.ResponseWriter, r *http.Request) error {
	limitReader := io.LimitReader(r.Body, ah.LimitBytes)
	body, err := ioutil.ReadAll(limitReader)
	if err != nil {
		return AdmissionError{false, "could not read the request body", err.Error()}
	}

	if body == nil || len(body) == 0 {
		return AdmissionError{
			false,
			"no request body was received",
			"the request body was nil/len == 0",
		}
	}

	incomingReview := admission.AdmissionReview{}
	if _, _, err := ah.deserializer.Decode(body, nil, &incomingReview); err != nil {
		return AdmissionError{false, "decoding the review request failed", err.Error()}
	}

	if incomingReview.Request == nil {
		return errors.New("received invalid AdmissionReview")
	}
	reviewResponse, err := ah.AdmitFunc(&incomingReview)
	if err != nil {
		return AdmissionError{false, err.Error(), reviewResponse.Result.Message}
	}

	reviewResponse.UID = incomingReview.Request.UID
	review := admission.AdmissionReview{
		Response: reviewResponse,
	}

	res, err := json.Marshal(&review)
	if err != nil {
		return AdmissionError{false, "marshalling the review response failed", err.Error()}
	}

	w.WriteHeader(http.StatusOK)
	w.Write(res)

	return nil
}
