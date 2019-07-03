package admissioncontrol

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// noopLogger is a no-op type that satifies the kit.Logger interface
type noopLogger struct{}

// Log logs nothing. Nada. Zilch.
func (nl *noopLogger) Log(keyvals ...interface{}) error {
	return nil
}

type testServer struct {
	srv    *AdmissionServer
	client *http.Client
	url    string
}

func newTestServer(ctx context.Context, t *testing.T) *testServer {
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "OK")
	})

	testSrv := httptest.NewUnstartedServer(testHandler)
	// We start the test server, copy its config out, and close it down so we can
	// start our own server. This is because httptest.Server only generates a
	// self-signed TLS config after starting it.
	testSrv.StartTLS()
	srv := &http.Server{
		Addr:      testSrv.Listener.Addr().String(),
		Handler:   testHandler,
		TLSConfig: testSrv.TLS.Clone(),
	}

	admissionServer, err := NewServer(srv, &noopLogger{})
	if err != nil {
		t.Fatalf("admission server creation failed: %s", err)
		return nil
	}
	testSrv.Close()

	go func() {
		if err := admissionServer.Run(ctx); err != nil {
			t.Logf("server stopped: %s", err)
		}
	}()

	// Wait for our listener to be ready for testing before we return a running
	// test server.
	var (
		backoffFactor = 1.25
		waitTime      = time.Millisecond * 50
		maxAttempts   = 5
		dialTimeout   = time.Second * 1
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		conn, err := net.DialTimeout(
			"tcp",
			admissionServer.srv.Addr,
			dialTimeout,
		)
		if err != nil {
			time.Sleep(waitTime)
			newWait := float64(waitTime) * math.Pow(backoffFactor, float64(attempt))
			waitTime = time.Duration(newWait)
			continue
		}

		if err := conn.Close(); err != nil {
			t.Fatalf("failed to close the test connection: %v", err)
		}

		break
	}

	return &testServer{srv: admissionServer, client: testSrv.Client(), url: testSrv.URL}
}

// Test that we can start a minimal AdmissionServer and handle a request.
func TestRun(t *testing.T) {
	t.Run("Server accepts HTTP requests", func(t *testing.T) {
		testSrv := newTestServer(context.TODO(), t)
		defer testSrv.srv.Stop()
		client := testSrv.client
		req, err := http.NewRequest(http.MethodGet, testSrv.url, nil)
		if err != nil {
			t.Fatalf("request creation failed: %s", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to make a request: %s", err)
		}

		if status := resp.StatusCode; status != http.StatusOK {
			t.Fatalf("unexpected status code: got %d (wanted %d)", status, http.StatusOK)
		}
	})

	t.Run("Context cancellation stops server", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		testSrv := newTestServer(ctx, t)
		testSrv.srv.GracePeriod = time.Microsecond * 1
		defer testSrv.srv.Stop()

		// Force a shutdown
		cancel()
		time.Sleep(testSrv.srv.GracePeriod)
		if err := testSrv.srv.srv.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			t.Fatalf(
				"server did not shutdown after a cancellation signal was received: got %v (want %v)",
				err,
				http.ErrServerClosed,
			)
		}

	})
}
