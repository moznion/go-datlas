module github.com/moznion/go-datlas/example

go 1.26.0

require github.com/moznion/go-datlas v0.0.0

require (
	github.com/goccy/go-yaml v1.19.2 // indirect
	golang.org/x/mod v0.43.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
)

replace github.com/moznion/go-datlas => ../

tool github.com/moznion/go-datlas/cmd/datlas
