package admissioncontrol

import (
	"context"
	"fmt"
	"golang.org/x/xerrors"
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
	// GracePeriod is defines how long the server allows for in-flight connections
	// to complete before exiting.
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
// The provided *http.Server must have its Handler field set, as well as a valid
// and non-nil TLSConfig. Kubernetes requires that Admission Controllers are
// only reachable over HTTPS (TLS), whether running in-cluster or externally.
func NewServer(srv *http.Server, logger log.Logger) (*AdmissionServer, error) {
	if srv == nil {
		return nil, xerrors.New("a non-nil *http.Server must be provided")
	}

	if logger == nil {
		return nil, xerrors.New("a non-nil log.Logger must be provided")
	}

	if srv.TLSConfig == nil {
		// Warn that TLS termination is required
		logger.Log(
			"msg", "the provided *http.Server has a nil TLSConfig. Admission webhooks must be served over TLS, or from behind a TLS-terminating proxy",
		)
	}

	as := &AdmissionServer{
		srv:         srv,
		logger:      logger,
		GracePeriod: defaultGracePeriod,
	}

	return as, nil
}

// Run the AdmissionServer; starting the configured *http.Server, and blocking
// indefinitely.
//
// Run will return under three explicit cases:
//
// 1. An interrupt (SIGINT; "Ctrl+C") or termination (SIGTERM) signal, such as
// the SIGTERM most process managers send: e.g. as Kubernetes sends to a Pod:
// https://kubernetes.io/docs/concepts/workloads/pods/pod/#termination-of-pods
//
// 2. When an error is returned from the listener on our server (fails to bind
// to a port, terminal network issue, etc.)
//
// 3. When we receive a cancellation signal from the parent context; e.g. by
// calling the returned CancelFunc from calling context.WithCancel(ctx)
//
// This allows us to stop accepting connections, allow in-flight connections to
// finish gracefully (up to the configured grace period), and then close the
// server. You may also call the .Stop() method on the server to trigger a
// shutdown.
func (as *AdmissionServer) Run(ctx context.Context) error {
	sigChan := make(chan os.Signal, 1)
	defer close(sigChan)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	errs := make(chan error)
	defer close(errs)
	go func() {
		// Start a plaintext listener if no TLSConfig is provided
		switch as.srv.TLSConfig {
		case nil:
			as.logger.Log(
				"msg", fmt.Sprintf("admission control listening on '%s' (plaintext HTTP)", as.srv.Addr),
			)

			if err := as.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errs <- err
				as.logger.Log(
					"err", err.Error(),
					"msg", "the server exited",
				)
				return
			}
		default:
			as.logger.Log(
				"msg", fmt.Sprintf("admission control listening on '%s' (TLS)", as.srv.Addr),
			)

			if err := as.srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				errs <- err
				as.logger.Log(
					"err", err.Error(),
					"msg", "the server exited",
				)
				return
			}
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

// Stop stops the AdmissionServer, if running, waiting for configured grace period.
func (as *AdmissionServer) Stop() error {
	return as.shutdown(context.TODO(), as.GracePeriod)
}
