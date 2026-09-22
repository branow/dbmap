module github.com/branow/dbmap

go 1.26.0

require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/jcmturner/gofork v1.7.6
	github.com/jcmturner/gokrb5/v8 v8.4.4
	github.com/microsoft/go-mssqldb v1.11.0
	github.com/spf13/cobra v1.10.2
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/term v0.46.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/anthropics/anthropic-sdk-go v1.72.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/goidentity/v6 v6.0.1 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/openai/openai-go/v3 v3.61.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// The llm module lives in this repo but ships its own go.mod so a second
// project can import it without dbmap's dependency graph. It is wired in by
// path here; a tagged require would be a release concern, not a build one.
require github.com/branow/dbmap/llm v0.0.0

replace github.com/branow/dbmap/llm => ./llm
