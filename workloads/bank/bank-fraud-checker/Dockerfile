# Build context must be the repo root (not this directory) — this repo is a
# single Go module (root go.mod/go.sum, no per-workload module), matching how
# `ko` already builds bank-client/bank-server. Unlike those, this workload
# isn't ko-buildable: it doesn't run in Kubernetes, it's pushed to ECR and run
# as a plain Docker container on a VM (see cofide-connect's
# node-observer-instance Terraform module) — no ARM64 requirement (unlike
# bank-agent), build for whatever platform that VM actually is.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bank-fraud-checker ./workloads/bank/bank-fraud-checker

FROM gcr.io/distroless/static-debian12
COPY --from=build /bank-fraud-checker /bank-fraud-checker
ENTRYPOINT ["/bank-fraud-checker"]
