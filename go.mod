module github.com/wurp/ourcloud-fcm-push-gateway

go 1.24.0

toolchain go1.24.5

require (
	github.com/go-chi/chi/v5 v5.0.12
	github.com/google/uuid v1.6.0
	github.com/wurp/friendly-backup-reboot/src/go/ourcloud-client v0.0.0
	github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto v0.0.0
	google.golang.org/protobuf v1.36.10
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/net v0.46.0 // indirect
	golang.org/x/sys v0.37.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251014184007-4626949a642f // indirect
	google.golang.org/grpc v1.75.1 // indirect
)

replace github.com/wurp/friendly-backup-reboot/src/go/ourcloud-client => ../friendly-backup-reboot/src/go/ourcloud-client

replace github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto => ../friendly-backup-reboot/src/go/ourcloud-proto
