package main

import (
	"fmt"
	"io"
	"runtime"
)

// These values are set by the release build. Plain go build remains a dev build.
var (
	appVersion = "dev"
	gitCommit  = "unknown"
	buildTime  = "unknown"
)

func runVersion(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || (args[0] != "version" && args[0] != "--version") {
		return false, 0
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: obtop version | obtop --version")
		return true, 2
	}
	fmt.Fprintf(stdout, "Version: %s\nGit Commit: %s\nBuild Time: %s\nGo Version: %s\nPlatform: %s/%s\n",
		appVersion, gitCommit, buildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return true, 0
}
