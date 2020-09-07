# Copyright (c) 2020 TriggerMesh Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.15-stretch AS builder

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
