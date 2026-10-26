package version

import (
	"fmt"
	"runtime"
)

// Version is the semantic version of the fm CLI.
var Version = "0.0.1"

// Commit is the git commit hash, optionally injected via -ldflags.
var Commit = "dev"

// BuildDate is the build timestamp, optionally injected via -ldflags.
var BuildDate = "unknown"

// Info holds structured version details.
type Info struct {
	Version   string `json:"version" yaml:"version"`
	Commit    string `json:"commit" yaml:"commit"`
	BuildDate string `json:"buildDate" yaml:"buildDate"`
	GoVersion string `json:"goVersion" yaml:"goVersion"`
	OS        string `json:"os" yaml:"os"`
	Arch      string `json:"arch" yaml:"arch"`
}

// Get returns the current runtime version information.
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// String returns a human-readable multi-platform version string.
func (i Info) String() string {
	return fmt.Sprintf("fm version %s (%s/%s, %s, commit: %s)", i.Version, i.OS, i.Arch, i.GoVersion, i.Commit)
}
