// Package version carries what this binary is, so "which build is running?"
// has an answer from the command line, from /metrics and from the page footer.
//
// The values are set at link time by -ldflags. A build that does not set them
// (go build, go test, an IDE) says so rather than pretending to be a release:
// a wrong version is worse than an obviously missing one.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Set by -ldflags="-X tide/internal/version.version=… -X …". See the release
// workflow and the Dockerfile.
var (
	version = ""
	commit  = ""
	date    = ""
)

// Info is everything known about this build.
type Info struct {
	// Version is the release tag ("v0.0.1-beta.1"), or "dev" for a build that
	// was not made by the release pipeline.
	Version string `json:"version"`
	// Commit is the full git SHA, or "" when it could not be determined.
	Commit string `json:"commit"`
	// Date is when the binary was built, RFC3339, or "".
	Date string `json:"date"`
	// Modified is true when the working tree had uncommitted changes.
	Modified bool   `json:"modified,omitempty"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
}

// Get resolves the build information, preferring what was linked in and
// falling back to what the Go toolchain stamped into the binary itself.
func Get() Info {
	i := Info{
		Version:  version,
		Commit:   commit,
		Date:     date,
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	// go build records VCS state on its own when building inside a checkout,
	// which covers everything the release pipeline did not build.
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Commit == "" {
					i.Commit = s.Value
				}
			case "vcs.time":
				if i.Date == "" {
					i.Date = s.Value
				}
			case "vcs.modified":
				i.Modified = s.Value == "true"
			}
		}
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	return i
}

// Short is one line for a log field or a footer: "v0.0.1-beta.1 (a1b2c3d)".
func (i Info) Short() string {
	var b strings.Builder
	b.WriteString(i.Version)
	if i.Commit != "" {
		b.WriteString(" (")
		b.WriteString(shortCommit(i.Commit))
		if i.Modified {
			b.WriteString("-dirty")
		}
		b.WriteString(")")
	}
	return b.String()
}

// String is the multi-line form `tide version` prints.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tide %s\n", i.Version)
	if i.Commit != "" {
		c := i.Commit
		if i.Modified {
			c += " (modified)"
		}
		fmt.Fprintf(&b, "commit:   %s\n", c)
	}
	if i.Date != "" {
		fmt.Fprintf(&b, "built:    %s\n", i.Date)
	}
	fmt.Fprintf(&b, "go:       %s\nplatform: %s\n", i.Go, i.Platform)
	return b.String()
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
