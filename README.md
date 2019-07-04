# Admission Control

🕵️🕵️🕵️

[![GoDoc](https://godoc.org/github.com/elithrar/admission-control?status.svg)](https://godoc.org/github.com/elithrar/admission-control)
[![CircleCI](https://circleci.com/gh/elithrar/admission-control.svg?style=svg)](https://circleci.com/gh/elithrar/admission-control)

A micro-framework for building Kubernetes [Admission Controllers](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/).

- Provides an extensible `AdmissionHandler` type that accepts a custom
  admission function (or `AdmitFunc`), making it easy for you to add new
  validating or mutating webhook endpoints.
- Makes it easy to set up: 
- Includes an example `AdmitFunc` for denying the creation of public Ingress
  and Services in GKE.
- Provides example `Deployment`, `Service` and `ValidatingWebhookConfiguration`
  definitions for you to build off of.

> **Note**: Looking to extend admission-control for your own uses? It's first-and-foremost exposed as a library - simply import it as `github.com/elithrar/admission-control` and reference the example server at `cmd/admissiond`.

## Setup & Pre-requisites

You'll need:

- Access to a Kubernetes cluster (GKE, minikube, AKS, etc) with support for admission webhooks (v1.9+)
- [`cfssl`](https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/#download-and-install-cfssl) as part of the process of generating a TLS key-pair, and some familiarity with creating TLS (SSL) certificates (CSRs, PEM-encoded certificates, keys).
- Experience writing Go - for implementing your own `AdmitFuncs` (refer to the example `DenyPublicServices` AdmitFunc included).
- Experience building OCI (Docker) containers via `docker build` or similar.

## Configuring a Server

> ⚠ *Note*: Admission webhooks must support HTTPS (TLS) connections; k8s does not allow webhooks to be reached over plain-text HTTP. If running in-cluster, the Service fronting the controller must be reachable via TCP port 443. External webhooks only need to satisfy the HTTPS requirement, but can be reached on any valid TCP port.

Having your k8s cluster create a TLS certificate for you will dramatically simplify the configuration, as self-signed certificates require you to provide a `.webhooks.clientConfig.caBundle` value for verification.

The key steps include:

1. Generate a TLS keypair for the admission controller by issuing a `CertificateSigningRequest` against the Kubernetes cluster, and obtain the CA certificate from the k8s cluster.
2. Creating a `Deployment` and a `Service` that makes the admission controller available to the cluster.
3. Creating a `ValidatingWebhookConfiguration` that points matching k8s API requests to a route on your admission controller. i.e. you may want to configure different validation policies between Services and Pods.

---

### Generating TLS Certificates

As noted above, we need to make our webhook endpoint available over HTTPS (TLS), which requires generating a CA cert (required as the `caBundle` value), key and certificate. You can can choose to [have your k8s cluster sign & provide a cert](https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/#create-a-certificate-signing-request) for you, or otherwise provide your own self-signed cert & CA cert.

We're going to have our cluster issue a certificate for us, which simplifies the process:

1. Create a k8s [`CertificateSigningRequest`](https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/#create-a-certificate-signing-request) for the hostname(s) you will deploy the Service as. There is an example CSR in `demo-certs/csr.yaml` for the `admission-control-service.default.svc` hostname. This hostname must match the `.webhooks.name[].clientConfig.service.name` described in your `ValidatingWebhookConfiguration`.

2. [Approve](https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/#approving-certificate-signing-requests) and then [fetch the certificate](https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/#download-the-certificate-and-use-it) from the k8s API server.

3. Create a `Secret` that [contains the TLS key-pair](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets) - the key you created alongside the CSR in step 1, and the certificate you fetched via `kubectl get csr <name> ...` - e.g. `kubectl create secret tls <name> --cert=cert.crt --key=key.key`.

4. Retrieve the k8s cluster CA cert - this will be the `.webhooks.clientConfig.caBundle` value in our `ValidatingWebhookConfiguration`: `

```sh
kubectl config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.certificate-authority-data}'
```

Specifically, you'll want to make sure your manifest looks like this:

```yaml
apiVersion: admissionregistration.k8s.io/v1beta1
kind: ValidatingWebhookConfiguration
metadata:
  name: deny-public-services
webhooks:
  - name: deny-public-services.questionable.services
    # <snip, for brevity>
    clientConfig:
      service:
        # This is the hostname our certificate needs in its Subject Alternative
        # Name array - name.namespace.svc
        name: admission-control-service
        namespace: default
        path: "/admission-control/deny-public-services"
      # This will be the CA cert from your k8s cluster, or the CA cert you
      # generated if you took the DIY approach.
      caBundle: "<your-base64-encoded-PEM-certificate-here>"
```

With the TLS certificates in hand, you can now move on to deploying the controller.

---

### Deploying the Admission Controller

With the TLS certificates generated & the associated `Secret` created, we can update our `Deployment`, `Service` and `ValidatingWebhookConfiguration` in-kind. Refer to the `samples/` directory if you need a reference config.

1. Create a `Deployment` and make sure the `-host` flag passed to the admissiond container matches the hostname (ServerName) you used in the CSR.
2. Update the `.spec.containers[].volumes.secret.secretName` to refer to the `Secret` you created in step 3.
3. Create a `Service` that exposes the `Deployment` in step no. 4 to the cluster. Remember: the name of the Service should match one of the names in step 1.
4. Create a `ValidatingWebhookConfiguration` that matches the objects (kinds, versions) and actions (create, update, delete), and configure the `.webhooks.clientConfig.service` map to point to the `Service` you created.

> Note: A set of example manifests - both `admissiond-deployment.yml` and `deny-public-admissions-config.yml`- are available in the `samples/` directory.

To deploy the built-in server to your cluster with its existing validation endpoints, you'll need to build the container image and push it to an image registry that your k8s cluster can access.

If you're using Google Container Registry, you can [push images to the same project](https://cloud.google.com/container-registry/docs/pushing-and-pulling) as your GKE cluster:

```sh
docker build -t yourco/admissiond .
docker tag yourco/admissiond gcr.io/$PROJECTNAME/admissiond
docker push gcr.io/$PROJECTNAME/admissiond
```

Make sure to update/copy `samples/admission-control-service.yaml` with the new container image URL before deploying it:

```sh
# An example Deployment we'll try to expose
kubectl apply -f samples/hello-app.yaml
# Install the Admission Controller into the cluster
kubectl apply -f samples/admission-control-service.yaml
# Add our ValidatingWebhookConfiguration
kubectl apply -f samples/deny-public-webhook-config.yaml
```

Let's now attempt to deploy a `kind: Service` of `type: LoadBalancer` without [the internal-only annotations](https://cloud.google.com/kubernetes-engine/docs/how-to/internal-load-balancing#overview):

```sh
kubectl apply -f samples/public-service.yaml
```

You should see the following output:

```sh
Error from server (hello-service does not have the cloud.google.com/load-balancer-type: Internal annotation.): error when creating "samples/public-service.yaml": admission webhook "deny-public-services.questionable.services" denied the request: Services of type: LoadBalancer without an internal annotation are not allowed on this cluster
```

Perfect! 🎉

### Extending Things

The core type of the library is the [`AdmitFunc`](https://godoc.org/github.com/elithrar/admission-control#AdmitFunc) - a function that takes a k8s `AdmissionReview` object and returns an `(*AdmissionResponse, error)` tuple.

You can then pass pass the `AdmissionHandler` to your favorite HTTP router, and define a path that the function is avaiable on:

```go
	r := mux.NewRouter().StrictSlash(true)
	admissions := r.PathPrefix("/admission-control").Subrouter()
	admissions.Handle("/deny-public-services", &admissioncontrol.AdmissionHandler{
		AdmitFunc:  admissioncontrol.DenyPublicServices,
		Logger:     logger,
    }).Methods(http.MethodPost)
```

The example server [`admissiond`](https://github.com/elithrar/admission-control/tree/master/examples/admissiond) provides a step-by-step of how to configure & serve your admission controller endpoints.

---

### Troubleshooting

If you run into problems setting up the admission-controller, make sure that:

- Your certificates are valid / key-pairs match
- You've inspected the Pod logs - e.g. via `kubectl logs -f -l app=admission-control` - all HTTP handler errors are logged to the configured logger.
- Your `ValidatingWebhookConfiguration` is matching the right API versions, namespaces & objects vs. what you have configured as an `AdmitFunc` endpoint in the admission-control server.

If you're stuck, open an issue with the output of:

```sh
kubectl version
# replace the label if you've authored your own Deployment manifest
kubectl logs -f -l app=admission-control
```

... and any relevant error messages from attempting to `kubectl apply -f <manifest>` that match your `ValidatingWebhookConfiguration`.

## Contributing

This project is open to contributions!

As a courtesy: please open an issue with a brief proposal of your
idea first (and the use-cases surrounding it!) before diving into
implementation.

## License

Apache 2.0 licensed. Copyright Google, LLC (2019). See the LICENSE file for details.
