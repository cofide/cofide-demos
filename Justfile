set shell := ["bash", "-euo", "pipefail", "-c"]

export KO_DOCKER_REPO := env_var_or_default("KO_DOCKER_REPO", "kind.local")
export KIND_CLUSTER_NAME := env_var_or_default("KO_DOCKER_REPO", "user")

namespace := "demo"
cert_dir := "certs"
key_file := cert_dir + "/server.key"
cert_file := cert_dir + "/server.crt"
secret_name := "tls-secret"

check-deps:
    # Check for demo script dependencies
    for cmd in openssl ko kubectl; do \
        if ! command -v $cmd &> /dev/null; then \
            echo "Error: $cmd is not installed" >&2; \
            exit 1; \
        fi \
    done

create-cert-dir: check-deps
    # Create certificate directory if it doesn't exist
    if [[ ! -d "{{cert_dir}}" ]]; then \
        mkdir -p "{{cert_dir}}"; \
        echo "Created directory {{cert_dir}}"; \
    fi

generate-cert: create-cert-dir
    # Create a self-signed, long-lived (10 year) TLS certificate for *demo purposes*
    openssl req -x509 \
        -newkey rsa:2048 \
        -keyout "{{key_file}}" \
        -out "{{cert_file}}" \
        -days 3650 \
        -nodes \
        -subj "/C=US/ST=State/L=City/O=Organization/CN=localhost"
    
    # Verify the certificate
    openssl x509 -in "{{cert_file}}" -text -noout > /dev/null

ensure-namespace:
    if ! kubectl get namespace "{{namespace}}" &> /dev/null; then \
        echo "Namespace {{namespace}} does not exist"; \
        read -p "Create namespace? (y/n) " -r; \
        echo; \
        if [[ $REPLY =~ ^[Yy]$ ]]; then \
            kubectl create namespace "{{namespace}}"; \
        else \
            echo "Aborting..."; \
            exit 1; \
        fi \
    fi

create-secret: ensure-namespace generate-cert
    # Create the secret
    kubectl create secret tls "{{secret_name}}" \
        --key "{{key_file}}" \
        --cert "{{cert_file}}" \
        -n "{{namespace}}"
    
    echo "Created Kubernetes secret {{secret_name}}"

# Build all demo ping-pong applications
build: build-ping-pong build-cofide-sdk-ping-pong build-ping-pong-mesh

# Build the legacy ping-pong applications
build-ping-pong:
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong/server
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong/client

# Build the ping-pong applications enhanced with the Cofide SDK
build-cofide-sdk-ping-pong:
  ko build -L github.com/cofide/cofide-demos/workloads/cofide-sdk/server
  ko build -L github.com/cofide/cofide-demos/workloads/cofide-sdk/client

# Build the ping-pong applications to be deployed in an Istio service mesh
build-ping-pong-mesh:
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong-mesh/server
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong-mesh/client

deploy-ping-pong: create-secret
    # Deploy ping-pong server (legacy)
    if ! ko resolve -f workloads/ping-pong/deploy.yaml | kubectl apply -n "{{namespace}}" -f -; then \
        echo "Error: Deployment failed" >&2; \
        exit 1; \
    fi; \
    echo "Deployment complete"

deploy-cofide-sdk: ensure-namespace
    # Deploy ping-pong server (cofide)
    if ! ko resolve -f workloads/cofide-sdk/deploy.yaml | kubectl apply -n "{{namespace}}" -f -; then \
        echo "Error: Deployment failed" >&2; \
        exit 1; \
    fi; \
    echo "Deployment complete"