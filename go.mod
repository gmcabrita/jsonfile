module github.com/gmcabrita/jsonfile

go 1.27.0

require hegel.dev/go/hegel v0.6.31

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/ebitengine/purego v0.11.0-alpha.6.0.20260707033313-5f49e7c49322 // indirect
	github.com/kisielk/errcheck v1.20.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260811182544-a038080d80e5 // indirect
	golang.org/x/tools v0.49.0 // indirect
	golang.org/x/vuln v1.7.0 // indirect
	honnef.co/go/tools v0.8.1 // indirect
	mvdan.cc/gofumpt v0.11.0 // indirect
)

tool (
	github.com/kisielk/errcheck
	golang.org/x/tools/cmd/goimports
	golang.org/x/vuln/cmd/govulncheck
	honnef.co/go/tools/cmd/staticcheck
	mvdan.cc/gofumpt
)
