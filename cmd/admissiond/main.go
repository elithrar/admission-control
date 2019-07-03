package main

import (
	"context"
	"crypto/tls"
	"flag"
	"github.com/gorilla/mux"
	stdlog "log"
	"net/http"
	"os"
	"time"

	admissioncontrol "github.com/elithrar/admission-control"
	log "github.com/go-kit/kit/log"
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
	stdlog.SetOutput(log.NewStdlibAdapter(logger))
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "loc", log.DefaultCaller)

	// TLS configuration
	keyPair, err := tls.LoadX509KeyPair(conf.TLSCertPath, conf.TLSKeyPath)
	if err != nil {
		fatal(logger, err)
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{keyPair},
		ServerName:   conf.Host,
	}

	// Set up the routes & logging middleware.
	r := mux.NewRouter().StrictSlash(true)

	r.HandleFunc("/healthz",
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
	).Methods(http.MethodGet)

	admissions := r.PathPrefix("/admission-control").Subrouter()
	admissions.Handle("/deny-public-services", &admissioncontrol.AdmissionHandler{
		AdmitFunc: admissioncontrol.DenyPublicServices,
		Logger:    logger,
	}).Methods(http.MethodPost)

	// HTTP server
	srv := &http.Server{
		Handler:           admissioncontrol.LoggingMiddleware(logger)(r),
		TLSConfig:         tlsConf,
		Addr:              ":" + conf.Port,
		IdleTimeout:       time.Second * 15,
		ReadTimeout:       time.Second * 15,
		ReadHeaderTimeout: time.Second * 15,
		WriteTimeout:      time.Second * 15,
	}

	admissionServer, err := admissioncontrol.NewServer(
		srv,
		log.With(logger, "component", "server"),
	)
	if err != nil {
		fatal(logger, err)
		return
	}

	if err := admissionServer.Run(ctx); err != nil {
		fatal(logger, err)
		return
	}
}

func fatal(logger log.Logger, err error) {
	logger.Log(
		"status", "fatal",
		"err", err,
	)

	os.Exit(1)
	return
}
