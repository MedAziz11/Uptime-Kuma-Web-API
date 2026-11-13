// Package version holds the version of uptime-kuma-api.
package version

// Version is reported by /health and in the OpenAPI document. Release
// builds set it at link time:
//
//	go build -ldflags "-X github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/version.Version=3.0.0" ./cmd/uptime-kuma-api
//
// It is a variable only because -X can set nothing else; nothing assigns it
// at run time.
var Version = "3.0.0-dev"
