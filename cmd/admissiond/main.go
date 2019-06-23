package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	admissioncontrol "github.com/elithrar/admission-control"
	log "github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
)

type conf struct {
	TLSCertPath string
	TLSKeyPath  string
	Port        string
	Host        string
}

func main() {
	ctx := context.Background()

	// Get config
	conf := &conf{}
	flag.StringVar(&conf.TLSCertPath, "cert-path", "./cert.crt", "The path to the PEM-encoded TLS certificate")
	flag.StringVar(&conf.TLSKeyPath, "key-path", "./key.key", "The path to the unencrypted TLS key.")
	flag.StringVar(&conf.Port, "port", "8443", "The port to listen on (HTTPS).")
	flag.StringVar(&conf.Host, "host", "admissiond.questionable.services", "The hostname for the service.")
	flag.Parse()

	// Set up logging
	var logger log.Logger
	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "loc", log.DefaultCaller)

	// Set up which

	// Set up the routes & logging middleware.
	r := mux.NewRouter().StrictSlash(true)

	r.HandleFunc("/healthz",
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
	).Methods(http.MethodGet)

	admissions := r.PathPrefix("/admission-control").Subrouter()
	admissions.Handle("/deny-public-services", &admissioncontrol.AdmissionHandler{
		AdmitFunc:  admissioncontrol.DenyPublicServices,
		Logger:     logger,
		LimitBytes: 1024 * 1024 * 1024, // 1MB,
	}).Methods(http.MethodPost)

	// TLS & HTTP server setup
	keyPair, err := tls.LoadX509KeyPair(conf.TLSCertPath, conf.TLSKeyPath)
	if err != nil {
		fatal(logger, err)
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{keyPair},
		ServerName:   conf.Host,
	}

	srv := &http.Server{
		Handler:           admissioncontrol.LoggingMiddleware(logger)(r),
		TLSConfig:         tlsConf,
		Addr:              ":" + conf.Port,
		IdleTimeout:       time.Second * 15,
		ReadTimeout:       time.Second * 15,
		ReadHeaderTimeout: time.Second * 15,
		WriteTimeout:      time.Second * 15,
	}

	go func() {
		logger.Log(
			"msg", fmt.Sprintf("admissiond listening on '%s:%s'", conf.Host, conf.Port),
		)
		if err := srv.ListenAndServeTLS("", ""); err != nil {
			fatal(logger, err)
		}
	}()

	// Graceful shutdown: block until we receive a signal.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-signalChan

	logger.Log(
		"msg", "shutting down server",
		"err", fmt.Sprintf("received signal: %s", sig.String()),
	)
	srv.Shutdown(ctx)
}

func fatal(logger log.Logger, err error) {
	logger.Log(
		"status", "fatal",
		"err", err,
	)

	os.Exit(1)
	return
}
