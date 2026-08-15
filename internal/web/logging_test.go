package web

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// TestMain silences the request log for the rest of the package. Every
// handler test drives the real Handler, and one line per request would bury
// the suite's own output. The test below points the log at a buffer of its
// own while it runs.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// captureLog redirects the standard logger for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(io.Discard)
		log.SetFlags(flags)
	})
	return &buf
}

// TestRequestLogNeverCarriesACardSecret is the reason this middleware needs
// care at all. The scan URL's path IS a write credential for the shelf, and
// a log file outlives the card: it would still name a revoked secret months
// later, in exactly the file a steward pastes into a support thread.
func TestRequestLogNeverCarriesACardSecret(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	now := time.Date(2026, time.September, 15, 16, 12, 0, 0, chicago)

	buf := captureLog(t)
	scanAt(t, st, tok.Secret, now)

	logged := buf.String()
	if strings.Contains(logged, tok.Secret) {
		t.Errorf("the request log names a card secret: %q", logged)
	}
	if !strings.Contains(logged, "GET /s/<redacted> 302") {
		t.Errorf("log line = %q, want a redacted scan at 302", logged)
	}
}

func TestRequestLogRecordsMethodPathAndStatus(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	h := New(st, time.Now).Handler()

	buf := captureLog(t)
	get(t, h, "/b/fairview/")
	get(t, h, "/b/nope/")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "GET /b/fairview/ 200 ") {
		t.Errorf("line = %q, want a 200 for the shelf", lines[0])
	}
	if !strings.HasPrefix(lines[1], "GET /b/nope/ 404 ") {
		t.Errorf("line = %q, want a 404 for the unknown slug", lines[1])
	}
}

func TestRedactPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C", "/s/<redacted>"},
		{"/s/", "/s/<redacted>"},
		// ServeMux is case-sensitive, so these never reach the scan handler
		// and 404 — but they still carry a real secret, and a request that
		// matched no route is the one a steward is most likely to go looking
		// at in the log afterwards.
		{"/S/K7QFM8X2N4TJ9WPR3VYB6HZQ5C", "/s/<redacted>"},
		{"/S/k7qfm8x2n4tj9wpr3vyb6hzq5c", "/s/<redacted>"},
		{"/b/fairview/", "/b/fairview/"},
		{"/b/fairview/about", "/b/fairview/about"},
		{"/", "/"},
		// Not a scan path; must not be redacted just for starting with s.
		{"/shelf", "/shelf"},
	} {
		if got := redactPath(tc.in); got != tc.want {
			t.Errorf("redactPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// statusWriter must report 200 for a handler that writes a body without
// ever calling WriteHeader, which is what net/http does implicitly.
func TestStatusWriterDefaultsTo200(t *testing.T) {
	buf := captureLog(t)
	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello")) //nolint:errcheck // test handler
	}))
	get(t, h, "/")
	if !strings.Contains(buf.String(), " 200 ") {
		t.Errorf("log line = %q, want status 200", buf.String())
	}
}
