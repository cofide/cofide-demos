build:
	just build-ping-pong
	just build-cofide-sdk

build-ping-pong:
	CGO_ENABLED=0 go build -o bin/ping-pong/server ./workloads/ping-pong/server/main.go
	CGO_ENABLED=0 go build -o bin/ping-pong/client ./workloads/ping-pong/client/main.go

	docker build -f ./workloads/ping-pong/server/Dockerfile.server -t cofide-demo-ping-pong-server .
	docker build -f ./workloads/ping-pong/client/Dockerfile.client -t cofide-demo-ping-pong-client .

build-cofide-sdk:
	CGO_ENABLED=0 go build -o bin/cofide-sdk/server ./workloads/cofide-sdk/server/main.go
	CGO_ENABLED=0 go build -o bin/cofide-sdk/client ./workloads/cofide-sdk/client/main.go

	docker build -f ./workloads/cofide-sdk/server/Dockerfile.server -t cofide-demo-cofide-sdk-server .	
	docker build -f ./workloads/cofide-sdk/client/Dockerfile.client -t cofide-demo-cofide-sdk-client .
