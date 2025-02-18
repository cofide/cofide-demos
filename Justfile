set export
set shell := ["bash", "-euo", "pipefail", "-c"]

export KO_DOCKER_REPO := env_var_or_default("KO_DOCKER_REPO", "kind.local")
export KIND_CLUSTER_NAME := env_var_or_default("KIND_CLUSTER_NAME", "kind")

check-deps:
    # Check for demo script dependencies
    for cmd in kind kubectl; do \
        if ! command -v $cmd &> /dev/null; then \
            echo "Error: $cmd is not installed" >&2; \
            exit 1; \
        fi \
    done
    echo "All dependencies installed"

# Build all demo applications
build-demos: build-ping-pong build-ping-pong-mesh build-aws-demo

build-aws-demo:
    docker buildx build -t $KO_DOCKER_REPO/analysis:latest -f workloads/aws/analysis/Dockerfile workloads/aws/analysis
    kind load docker-image $KO_DOCKER_REPO/analysis:latest --name $KIND_CLUSTER_NAME
    # and consumer
    docker buildx build -t $KO_DOCKER_REPO/consumer:latest -f workloads/aws/consumer/Dockerfile workloads/aws/consumer
    kind load docker-image $KO_DOCKER_REPO/consumer:latest --name $KIND_CLUSTER_NAME

# Build the ping-pong application
build-ping-pong:
    docker buildx build -t $KO_DOCKER_REPO/server:latest -f workloads/ping-pong/server/Dockerfile workloads/ping-pong/server
    kind load docker-image $KO_DOCKER_REPO/server:latest --name $KIND_CLUSTER_NAME
    docker buildx build -t $KO_DOCKER_REPO/client:latest -f workloads/ping-pong/client/Dockerfile workloads/ping-pong/client
    kind load docker-image $KO_DOCKER_REPO/client:latest --name $KIND_CLUSTER_NAME

# Build the HTTP ping-pong applications to be deployed in a service mesh
build-ping-pong-mesh:
    docker buildx build -t $KO_DOCKER_REPO/server:latest -f workloads/ping-pong-mesh/server/Dockerfile workloads/ping-pong-mesh/server
    kind load docker-image $KO_DOCKER_REPO/server:latest --name $KIND_CLUSTER_NAME
    docker buildx build -t $KO_DOCKER_REPO/client:latest -f workloads/ping-pong-mesh/client/Dockerfile workloads/ping-pong-mesh/client
    kind load docker-image $KO_DOCKER_REPO/client:latest --name $KIND_CLUSTER_NAME

# this uses more fully qualified names to avoid clashes between different workloads
build verb='all':
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{ verb }}" = "all" ]; then
        find workloads -type d | while read -r path; do
            if [ -f "$path/Dockerfile" ]; then
                echo "Building $path"
                repo_name=$(echo "$path" | sed 's|^workloads/||' | tr '/' '_' | tr '-' '_')
                docker buildx build -t $KO_DOCKER_REPO/$repo_name:latest -f $path/Dockerfile $path
                kind load docker-image $KO_DOCKER_REPO/$repo_name:latest --name $KIND_CLUSTER_NAME
            fi
        done
    else
        path="workloads/$verb"
        if [ -d "$path" ] && [ -f "$path/Dockerfile" ]; then
            echo "Building $path"
            repo_name=$(echo "$path" | sed 's|^workloads/||' | tr '/' '_' | tr '-' '_')
            docker buildx build -t $KO_DOCKER_REPO/$repo_name:latest -f $path/Dockerfile $path
            kind load docker-image $KO_DOCKER_REPO/$repo_name:latest --name $KIND_CLUSTER_NAME
        else
            echo "Error: Directory or Dockerfile for $verb not found" >&2
            exit 1
        fi
    fi