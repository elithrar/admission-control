FROM golang:1.12 as build

LABEL repo="https://github.com/elithrar/admission-control"
ARG GIT_COMMIT=""
LABEL commit=$GIT_COMMIT
ENV GIT_COMMIT=$GIT_COMMIT

WORKDIR /go/src/app
COPY go.mod .
COPY go.sum .

ENV GO111MODULE=on
#ENV GOPROXY="https://proxy.golang.org"
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go install -v ./...

FROM gcr.io/distroless/base
COPY --from=build /go/bin/admissiond /

# Port 8080 is the default port for Cloud Run as per
# https://cloud.google.com/run/docs/reference/container-contract
EXPOSE 8080

CMD ["/admissiond", "-port=8080", "-http-only"]
