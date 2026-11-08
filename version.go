package main

import (
	"encoding/json"
	"io"
	"runtime"
)

// Set only by the release builder; a plain go build remains visibly unversioned.
var buildVersion = "dev"
var buildCommit = "unknown"
var buildSourceState = "unknown"

func printBuildInfo(dst io.Writer) error {
	return json.NewEncoder(dst).Encode(map[string]string{
		"version":      buildVersion,
		"commit":       buildCommit,
		"source_state": buildSourceState,
		"go_version":   runtime.Version(),
	})
}
