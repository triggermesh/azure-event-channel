FROM golang:1.14-stretch AS builder

ENV CGO_ENABLED 0
ENV GOOS linux
ENV GOARCH amd64

WORKDIR /go/src/azure-event-channel

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN BIN_OUTPUT_DIR=/bin make azure-event-channel-controller && \
    rm -rf ${GOPATH} && \
    rm -rf ${HOME}/.cache

FROM registry.access.redhat.com/ubi8/ubi-minimal

LABEL name "Triggermesh Channel Controller for Azure Events"
LABEL vendor "Triggermesh"
LABEL version "v0.1.0"
LABEL release "1"
LABEL summary "Triggermesh Channel Controller for Azure Events"
LABEL description "This is the Triggermesh Knative Channel Controller for Azure Events"

COPY LICENSE /LICENSE
COPY --from=builder /bin/azure-event-channel-controller /

ENTRYPOINT ["/azure-event-channel-controller"]
