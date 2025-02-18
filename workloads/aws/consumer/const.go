package main

const (
	address       = "0.0.0.0:9090"
	audience      = "consumer-workload"
	sessionName   = "consumer-workload-session"
	socketPath    = "unix:///spiffe-workload-api/spire-agent.sock"
	tokenFilePath = "./token"
)
