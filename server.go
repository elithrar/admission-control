package admissioncontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/go-kit/kit/log"
)

var (
	defaultGracePeriod = time.Second * 15
)

// AdmissionServer represents a HTTP server configuration for serving an
// Admission Controller.
//
// Use NewServer to create a new AdmissionServer.
type AdmissionServer struct {
	srv    *http.Server
	logger log.Logger
	// How long to wait for the server to complete in-flight requests when shutting
	// down.
	GracePeriod time.Duration
}

func (as *AdmissionServer) shutdown(ctx context.Context, gracePeriod time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, gracePeriod)
	defer cancel()
	as.logger.Log(
		"msg", "server shutting down",
	)
	return as.srv.Shutdown(timeoutCtx)
}

// NewServer creates an unstarted AdmissionServer, ready to be started (via the 'Run' method).
//
// The provided *http.Server must have its Handler & TLSConfig fields set.
func NewServer(srv *http.Server, logger log.Logger) (*AdmissionServer, error) {
	if srv == nil {
		return nil, errors.New("a *http.Server must be provided")
	}

	if srv.TLSConfig == nil {
		return nil, errors.New("a non-nil *http.Server.TLSConfig is required to start this server")
	}

	if logger == nil {
		return nil, errors.New("a non-nil log.Logger must be provided")
	}

	as := &AdmissionServer{
		srv:    srv,
		logger: logger,
		// GracePeriod is how long the server allows for in-flight connections to
		// complete before exiting.
		GracePeriod: defaultGracePeriod,
	}

	return as, nil
}

// Run the AdmissionServer; starting the configured *http.Server.
//
// Run will block indefinitely; and return under three explicit cases:
//
// 1. An interrupt (SIGINT; "Ctrl+C") or termination (SIGTERM) signal, such as
// the SIGTERM most process managers send: e.g. as Kubernetes sends to a Pod:
// https://kubernetes.io/docs/concepts/workloads/pods/pod/#termination-of-pods
// 2. When an error is returned from the listener on our server (fails to bind
// to a port, terminal network issue, etc.)
// 3. When we receive a cancellation signal from the parent context; e.g. by
// calling the returned CancelFunc from calling context.WithCancel(ctx)
//
// This allows us to stop accepting connections, allow in-flight connections to
// finish gracefully (up to the configured grace period), and then close the
// server.
func (as *AdmissionServer) Run(ctx context.Context) error {
	sigChan := make(chan os.Signal, 1)
	defer close(sigChan)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// run in goroutine
	errs := make(chan error)
	defer close(errs)
	go func() {
		as.logger.Log(
			"msg", fmt.Sprintf("admission control listening on '%s'", as.srv.Addr),
		)
		if err := as.srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errs <- err
			as.logger.Log(
				"err", err.Error(),
				"msg", "the server exited",
			)
			return
		}
		return
	}()

	// Block indefinitely until we receive an interrupt, cancellation or error
	// signal.
	for {
		select {
		case sig := <-sigChan:
			as.logger.Log(
				"msg", fmt.Sprintf("signal received: %s", sig),
			)
			return as.shutdown(ctx, as.GracePeriod)
		case err := <-errs:
			as.logger.Log(
				"msg", fmt.Sprintf("listener error: %s", err),
			)
			// We don't need to explictly call shutdown here, as
			// *http.Server.ListenAndServe closes the listener when returning an error.
			return err
		case <-ctx.Done():
			as.logger.Log(
				"msg", fmt.Sprintf("cancellation received: %s", ctx.Err()),
			)
			return as.shutdown(ctx, as.GracePeriod)
		}
	}
}

// Stop stops the AdmissionServer, if running.
func (as *AdmissionServer) Stop() error {
	return as.shutdown(context.TODO(), defaultGracePeriod)
}
