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

func TestValidateSubmissionRejectsBadLinks(t *testing.T) {
	for _, payload := range []string{
		"",
		"   ",
		"example.org",         // no scheme
		"ftp://example.org",   // wrong scheme
		"javascript:alert(1)", // not a fetchable scheme, and hostile
		"https://",            // no host
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

func TestValidateSubmissionNoteAndAttributionLength(t *testing.T) {
	over := linkSubmission()
	over.Note = strings.Repeat("a", MaxNoteRunes+1)
	if _, errs := ValidateSubmission(over); errs["note"] == "" {
		t.Error("over-long note accepted")
	}

	over = linkSubmission()
	over.Attribution = strings.Repeat("a", MaxAttributionRunes+1)
	if _, errs := ValidateSubmission(over); errs["attribution"] == "" {
		t.Error("over-long attribution accepted")
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
