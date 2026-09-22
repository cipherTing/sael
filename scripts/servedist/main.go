// Command servedir serves a directory tree over plain HTTP for the installer
// self-test.
//
// Why this exists at all: install.sh and install.ps1 fetch from
// SAEL_RELEASE_BASE_URL with curl and Invoke-WebRequest, and there is no way to
// point either of them at a local directory. The self-test therefore serves a
// dist/ built from the current commit and installs from it, so the whole install
// path -- download, checksum verification, extraction, linking -- is exercised
// against the real scripts without publishing a release first. Without that, the
// only way to test an installer is to cut a release, which means the installer is
// tested after the point where a bug is cheap to fix.
//
// Why Go rather than `python3 -m http.server`: the self-test runs on three
// runners, and python3 is not part of the Windows image's guaranteed tool set,
// while the Go toolchain is installed by every job that needs this server. A
// server that is missing on one platform turns the install test into a skipped
// test, and a skipped test looks the same as a passing one on the checks page --
// which is the specific failure mode this whole workflow exists to prevent.
//
// Usage: servedir <dir> <port>
//
// It prints one line, "listening on http://127.0.0.1:<port>", once the socket is
// bound, and then blocks. Callers wait for that line instead of sleeping, so a
// slow start cannot race the first download. Pass port 0 to let the kernel pick
// one: a fixed port turns a leftover server from an earlier run into a failure
// that has nothing to do with the installer.
//
// It binds to loopback only. It is reachable for the duration of one test job and
// serves a directory that is public build output anyway, but there is no reason
// for it to be reachable from off the machine.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: servedir <dir> <port>")
		os.Exit(2)
	}
	dir, port := os.Args[1], os.Args[2]

	// Bound before the listening line is printed, so a caller that waits for that
	// line never races the accept loop: once it is printed, a request cannot be
	// refused for want of a listener.
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		log.Fatalf("servedir: %v", err)
	}

	// http.FileServer refuses a path that escapes the root, which keeps the
	// negative cases honest: an installer asking for something outside the served
	// tree gets a 404 rather than a file.
	fmt.Printf("listening on http://%s\n", ln.Addr())
	_ = os.Stdout.Sync()

	if err := http.Serve(ln, http.FileServer(http.Dir(dir))); err != nil {
		log.Fatalf("servedir: %v", err)
	}
}
