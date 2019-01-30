# cf-report-memory-usage

CloudFoundry CLI plugin report on disk usage vs requested in a CloudFoundry installation

## Usage

```bash
cf report-memory-usage
```

## Development

```bash
go install . && \
    cf install-plugin ${GOPATH:-$HOME/go}/bin/cf-report-memory-usage -f && \
    cf report-memory-usage
```

## Building a new release

```bash
PLUGIN_PATH=${GOPATH:-$HOME/go}/src/github.com/govau/cf-report-memory-usage
PLUGIN_NAME=$(basename $PLUGIN_PATH)

GOOS=linux GOARCH=amd64 go build -o ${PLUGIN_NAME}.linux64 main-report-memory-usage.go
GOOS=linux GOARCH=386 go build -o ${PLUGIN_NAME}.linux32 main-report-memory-usage.go
GOOS=windows GOARCH=amd64 go build -o ${PLUGIN_NAME}.win64 main-report-memory-usage.go
GOOS=windows GOARCH=386 go build -o ${PLUGIN_NAME}.win32 main-report-memory-usage.go
GOOS=darwin GOARCH=amd64 go build -o ${PLUGIN_NAME}.osx main-report-memory-usage.go

shasum -a 1 ${PLUGIN_NAME}.*
```
