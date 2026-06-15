module github.com/lightning-rider-999/go-stashapp

go 1.26.4

require (
	github.com/spf13/cobra v1.10.2
	github.com/vektah/gqlparser/v2 v2.5.34
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)

require (
	github.com/Khan/genqlient v0.8.1
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/alexflint/go-arg v1.5.1 // indirect
	github.com/alexflint/go-scalar v1.2.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.6.1 // indirect
	github.com/gorilla/websocket v1.5.3
	github.com/kr/text v0.2.0 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0
	golang.org/x/tools v0.46.0 // indirect; A2: forced >= v0.46.0 — genqlient v0.8.1 pins v0.24.0, which fails to build under Go 1.26 (tokeninternal.go: invalid array length). 'go get -tool' re-pins it to the broken version, so re-bump after.
	gopkg.in/yaml.v2 v2.4.0
)

tool github.com/Khan/genqlient
