build: build-ping-pong build-cofide-sdk-ping-pong build-ping-pong-mesh

build-ping-pong:
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong/server
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong/client

build-cofide-sdk-ping-pong:
  ko build -L github.com/cofide/cofide-demos/workloads/cofide-sdk/server
  ko build -L github.com/cofide/cofide-demos/workloads/cofide-sdk/client

build-ping-pong-mesh:
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong-mesh/server
  ko build -L github.com/cofide/cofide-demos/workloads/ping-pong-mesh/client
