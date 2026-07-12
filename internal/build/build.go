// Package build provides build-time information about the application.
package build

import (
	"encoding/json"
	"strconv"
)

// set by build.sh
var (
	name               string
	version            string
	contactURL         string
	defaultLogLevel    string
	serviceEnabled     string
	serviceDesc        string
	serviceArgs        string
	serviceDefaultPort string
	// cosign keyless identity that signed the release artifacts. Empty in
	// local builds; remote update refuses to run without it.
	certIdentity string
	oidcIssuer   string
	testMode     string
)

type BuildInfo struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	ContactURL         string `json:"contactURL"`
	DefaultLogLevel    string `json:"defaultLogLevel"`
	ServiceEnabled     bool   `json:"serviceEnabled"`
	ServiceDesc        string `json:"serviceDesc"`
	ServiceArgs        string `json:"serviceArgs"`
	ServiceDefaultPort int    `json:"serviceDefaultPort"`
	// CertIdentity / OidcIssuer pin the cosign keyless identity used to
	// verify the update script before remote updates execute it.
	CertIdentity string `json:"certIdentity"`
	OidcIssuer   string `json:"oidcIssuer"`
	// TestMode bypasses HTTP auth, isolates storage in "-test" dirs, and
	// forces debug logging. Only ever true in local dev builds
	// (build.sh --test); production builds never set it.
	TestMode bool `json:"testMode"`
}

// PrintJSON prints the build info as JSON to stdout
func (b BuildInfo) PrintJSON() string {
	data, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	return string(data)
}

func Info() BuildInfo {
	port, err := strconv.Atoi(serviceDefaultPort)
	if err != nil {
		// fallback to 8080
		port = 8080
	}
	logLevel := defaultLogLevel
	if logLevel == "" {
		// fallback to DEBUG
		logLevel = "DEBUG"
	}
	return BuildInfo{
		Name:               name,
		Version:            version,
		ContactURL:         contactURL,
		DefaultLogLevel:    logLevel,
		ServiceEnabled:     serviceEnabled == "true",
		ServiceDesc:        serviceDesc,
		ServiceArgs:        serviceArgs,
		ServiceDefaultPort: port,
		CertIdentity:       certIdentity,
		OidcIssuer:         oidcIssuer,
		TestMode:           testMode == "true",
	}
}
