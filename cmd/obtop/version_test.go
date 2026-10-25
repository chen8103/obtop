package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionDispatch(t *testing.T) {
	for _, name := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		handled, code := runVersion([]string{name}, &out, &errOut)
		if !handled || code != 0 || errOut.Len() != 0 {
			t.Fatalf("%s: handled=%v code=%d stderr=%s", name, handled, code, &errOut)
		}
		for _, label := range []string{"Version: ", "Git Commit: ", "Build Time: ", "Go Version: ", "Platform: "} {
			if !strings.Contains(out.String(), label) {
				t.Errorf("missing %q in %s", label, &out)
			}
		}
		out.Reset()
		handled, code = runVersion([]string{name, "unexpected"}, &out, &errOut)
		if !handled || code != 2 || out.Len() != 0 {
			t.Fatalf("invalid version arguments accepted")
		}
	}
	for _, args := range [][]string{nil, {"snapshot"}, {"-h"}} {
		if handled, _ := runVersion(args, &bytes.Buffer{}, &bytes.Buffer{}); handled {
			t.Fatalf("intercepted unrelated arguments %v", args)
		}
	}
}
