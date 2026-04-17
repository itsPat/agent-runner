module github.com/itsPat/agent-runner/apps/runner

go 1.24.0

replace github.com/itsPat/agent-runner/gen/go => ../../gen/go

require github.com/itsPat/agent-runner/gen/go v0.0.0-00010101000000-000000000000

require (
	connectrpc.com/connect v1.19.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
