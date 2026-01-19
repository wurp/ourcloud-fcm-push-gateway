module github.com/wurp/ourcloud-fcm-push-gateway

go 1.24.0

toolchain go1.24.5

require (
	github.com/go-chi/chi/v5 v5.0.12
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/wurp/friendly-backup-reboot/src/go/ourcloud-client => ../friendly-backup-reboot/src/go/ourcloud-client

replace github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto => ../friendly-backup-reboot/src/go/ourcloud-proto
