FROM golang:1.15 as build

LABEL repo="https://github.com/vendasta/admission-control"
ARG GIT_COMMIT=""
LABEL commit=$GIT_COMMIT
ENV GIT_COMMIT=$GIT_COMMIT

WORKDIR /go/src/app
COPY go.mod .
COPY go.sum .

RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go install -v ./...

FROM gcr.io/distroless/base
COPY --from=build /go/bin/server /
EXPOSE 8443

CMD ["/server", "-cert-path", "certs/tls.crt", "-key-path", "certs/tls.key", "-port", "8443"]
