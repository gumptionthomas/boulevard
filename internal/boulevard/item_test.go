package boulevard

import (
	"strings"
	"testing"
)

func linkSubmission() Submission {
	return Submission{
		Type:        "link",
		Payload:     "https://example.org/thing",
		Note:        "Reminded me of the alley cat.",
		Attribution: "the guy with the beagle",
	}
}

func TestValidateSubmissionAcceptsAGoodLink(t *testing.T) {
	got, errs := ValidateSubmission(linkSubmission())
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if got.Type != "link" || got.Payload != "https://example.org/thing" {
		t.Errorf("normalized = %+v", got)
	}
}

func TestValidateSubmissionRequiresANote(t *testing.T) {
	// §3: the note is mandatory and is the point.
	for _, note := range []string{"", "   ", "\t\n "} {
		in := linkSubmission()
		in.Note = note
		_, errs := ValidateSubmission(in)
		if errs["note"] == "" {
			t.Errorf("note %q accepted, want a note error", note)
		}
	}
}

func TestValidateSubmissionTrimsAndKeepsWhatWasTyped(t *testing.T) {
	in := linkSubmission()
	in.Note = "  a real reason  "
	in.Attribution = "  Ruth  "
	got, errs := ValidateSubmission(in)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if got.Note != "a real reason" {
		t.Errorf("Note = %q, want it trimmed", got.Note)
	}
	if got.Attribution != "Ruth" {
		t.Errorf("Attribution = %q, want it trimmed", got.Attribution)
	}
}

// A payload with no scheme at all gets https, because typing "https://" on a
// phone keyboard while standing at a box is real friction and the shelf's
// own acceptance runs hit it on every link.
func TestValidateSubmissionAssumesHTTPSForABareDomain(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"example.org", "https://example.org"},
		{"example.org/thing", "https://example.org/thing"},
		{"www.example.org/a/b?c=d", "https://www.example.org/a/b?c=d"},
		{"  example.org/thing  ", "https://example.org/thing"},
		// Already carrying a scheme: left exactly as typed, including
		// http, which a steward may mean on a LAN box.
		{"https://example.org/thing", "https://example.org/thing"},
		{"http://example.org/thing", "http://example.org/thing"},
	} {
		in := linkSubmission()
		in.Payload = tc.typed
		got, errs := ValidateSubmission(in)
		if len(errs) != 0 {
			t.Errorf("payload %q: errors = %v, want none", tc.typed, errs)
			continue
		}
		if got.Payload != tc.want {
			t.Errorf("payload %q normalized to %q, want %q", tc.typed, got.Payload, tc.want)
		}
	}
}

// The scheme test is textual and conservative on purpose. Prepending
// https:// to anything whose scheme merely is not http/https would turn
// javascript:alert(1) into https://javascript:alert(1) — an href far worse
// than the rejection it replaced.
func TestValidateSubmissionRejectsBadLinks(t *testing.T) {
	for _, payload := range []string{
		"",
		"   ",
		"ftp://example.org",          // wrong scheme
		"javascript:alert(1)",        // hostile, and must never be prepended to
		"data:text/html,<script>x",   // likewise
		"mailto:someone@example.org", // not a web address
		"https://",                   // no host
		"//example.org",              // protocol-relative: no host once resolved
		// A bare host:port is indistinguishable from a scheme by grammar
		// alone (Go parses "example.org:8080/x" with scheme "example.org"),
		// so it keeps the old message telling the leaver to type the
		// protocol. Rare enough on a shelf to be worth the safety.
		"example.org:8080/thing",
	} {
		in := linkSubmission()
		in.Payload = payload
		_, errs := ValidateSubmission(in)
		if errs["payload"] == "" {
			t.Errorf("payload %q accepted, want a payload error", payload)
		}
	}
}

func TestValidateSubmissionTextLength(t *testing.T) {
	ok := linkSubmission()
	ok.Type = "text"
	ok.Payload = strings.Repeat("é", MaxTextRunes) // runes, not bytes
	if _, errs := ValidateSubmission(ok); errs["payload"] != "" {
		t.Errorf("text at the limit rejected: %v", errs)
	}

	over := ok
	over.Payload = strings.Repeat("é", MaxTextRunes+1)
	if _, errs := ValidateSubmission(over); errs["payload"] == "" {
		t.Error("text one rune over the limit accepted")
	}
}

// Both sides of both limits. `>` versus `>=` is the classic silent
// off-by-one, and a cap that rejects a note exactly at the limit is a
// composed note lost for no reason — which §6.2 calls this form's worst
// failure. Multi-byte on purpose: the limits are runes, not bytes.
func TestValidateSubmissionNoteAndAttributionLength(t *testing.T) {
	at := linkSubmission()
	at.Note = strings.Repeat("é", MaxNoteRunes)
	if _, errs := ValidateSubmission(at); errs["note"] != "" {
		t.Errorf("a note exactly at the limit was rejected: %v", errs)
	}

	over := linkSubmission()
	over.Note = strings.Repeat("é", MaxNoteRunes+1)
	if _, errs := ValidateSubmission(over); errs["note"] == "" {
		t.Error("a note one rune over the limit was accepted")
	}

	at = linkSubmission()
	at.Attribution = strings.Repeat("é", MaxAttributionRunes)
	if _, errs := ValidateSubmission(at); errs["attribution"] != "" {
		t.Errorf("an attribution exactly at the limit was rejected: %v", errs)
	}

	over = linkSubmission()
	over.Attribution = strings.Repeat("é", MaxAttributionRunes+1)
	if _, errs := ValidateSubmission(over); errs["attribution"] == "" {
		t.Error("an attribution one rune over the limit was accepted")
	}
}

func TestValidateSubmissionRejectsUnknownAndDeferredTypes(t *testing.T) {
	for _, typ := range []string{"", "video", "IMAGE", "image"} {
		in := linkSubmission()
		in.Type = typ
		_, errs := ValidateSubmission(in)
		if errs["type"] == "" {
			t.Errorf("type %q accepted; image is deferred to its own milestone", typ)
		}
	}
}

func TestValidateSubmissionReportsEveryBadFieldAtOnce(t *testing.T) {
	// A form that reveals one error per submit is a form nobody finishes
	// standing outside in the cold.
	_, errs := ValidateSubmission(Submission{Type: "nope", Payload: "", Note: ""})
	for _, field := range []string{"type", "note"} {
		if errs[field] == "" {
			t.Errorf("no error reported for %q; want all bad fields at once", field)
		}
	}
}

func TestAttributionIsOptional(t *testing.T) {
	in := linkSubmission()
	in.Attribution = ""
	if _, errs := ValidateSubmission(in); len(errs) != 0 {
		t.Errorf("errors = %v, want none — attribution is optional", errs)
	}
}
