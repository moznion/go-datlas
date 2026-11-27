module github.com/moznion/go-datlas/example

go 1.26.0

require (
	github.com/jackc/pgx/v5 v5.13.0
	github.com/moznion/go-datlas v0.0.0
)

require (
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	golang.org/x/mod v0.43.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.53.0 // indirect
)

replace github.com/moznion/go-datlas => ../

tool github.com/moznion/go-datlas/cmd/datlas
