build-docker:
	docker build -f ./workloads/ping-pong/server/Dockerfile.server -t cofide-demo-ping-pong-server ./workloads/ping-pong/server
	docker build -f ./workloads/ping-pong/client/Dockerfile.client -t cofide-demo-ping-pong-client ./workloads/ping-pong/client

