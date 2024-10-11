build-docker:
	docker build -f ./workload/server/Dockerfile.server -t cofide-demo-server ./workload/server
	docker build -f ./workload/client/Dockerfile.client -t cofide-demo-client ./workload/client

