// Package version reports the API contract version and the build version.
package version

import "runtime/debug"

// API is the version of the HTTP API contract. It goes into the OpenAPI
// document. Increase it by hand when the contract changes.
const API = "0.0.1"

// Build returns the version of the running binary, for example
// "v0.0.1-cc5b518". The Go build tool stamps the git revision. The revision is
// absent under "go run" and "go test", and the result is then "v0.0.1-devel".
func Build() string {
	hash, modified := revision()
	if hash == "" {
		return "v" + API + "-devel"
	}
	build := "v" + API + "-" + hash
	if modified {
		build += "-dirty"
	}
	return build
}

func revision() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var hash string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			hash = setting.Value
			if len(hash) > 7 {
				hash = hash[:7]
			}
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return hash, modified
}
