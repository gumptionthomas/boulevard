package boulevard

import (
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// ItemType is what was left. `image` exists in the vocabulary so the
// milestone that builds uploads migrates nothing, but ValidateSubmission
// rejects it — there is no upload path yet.
type ItemType string

const (
	ItemLink  ItemType = "link"
	ItemText  ItemType = "text"
	ItemImage ItemType = "image"
)

// ItemState tracks an item through the shelf.
//
// `pending` is not in DESIGN.md §3's list, which names only shelved, shed
// and released. §5 requires an approval queue, and a queue is a state; the
// spec amends §3 to add it.
type ItemState string

const (
	ItemPending  ItemState = "pending"
	ItemShelved  ItemState = "shelved"
	ItemShed     ItemState = "shed"
	ItemReleased ItemState = "released"
)

// Limits. The text and attribution caps come from the spec. The note cap is
// a judgment call: §3 makes the note required but names no bound, and an
// unbounded required field is a trivial way to fill a steward's disk.
// Counted in runes so every script gets the same allowance.
const (
	MaxNoteRunes        = 1000
	MaxTextRunes        = 4000
	MaxAttributionRunes = 120
)

// Item is one thing on the shelf.
//
// There is deliberately no session or author reference. Sessions are never
// swept, so such a field would be a durable link between everything one
// person left in a 24-hour window — the user record §1 forbids. Milestone 3
// counts leaves on the session row instead, which counts without linking.
type Item struct {
	ID          string
	LibraryID   LibraryID
	Type        ItemType
	Payload     string
	Note        string
	Attribution string
	CopiesTotal int
	CopiesLeft  int
	State       ItemState
	Pinned      bool
	Views       int
	Takes       int
	LeftAt      time.Time
	ShelvedAt   *time.Time
}

// Submission is what the leave form posted, before validation.
type Submission struct {
	Type        string
	Payload     string
	Note        string
	Attribution string
}

// FieldErrors maps a form field name to a message for the person who typed
// it. Empty means the submission is good.
type FieldErrors map[string]string

// ValidateSubmission trims, checks, and returns the normalized submission
// alongside any field errors.
//
// It reports every bad field at once rather than stopping at the first: a
// form that reveals one problem per submit is a form nobody finishes while
// standing outside in the cold.
func ValidateSubmission(in Submission) (Submission, FieldErrors) {
	out := Submission{
		Type:        strings.TrimSpace(in.Type),
		Payload:     strings.TrimSpace(in.Payload),
		Note:        strings.TrimSpace(in.Note),
		Attribution: strings.TrimSpace(in.Attribution),
	}
	errs := FieldErrors{}

	switch ItemType(out.Type) {
	case ItemLink:
		validateLink(out.Payload, errs)
	case ItemText:
		if out.Payload == "" {
			errs["payload"] = "Add the text you want to leave."
		} else if utf8.RuneCountInString(out.Payload) > MaxTextRunes {
			errs["payload"] = "That is longer than a shelf can hold. Trim it a little."
		}
	default:
		errs["type"] = "Choose a link or some text."
	}

	switch {
	case out.Note == "":
		errs["note"] = "Say why you are leaving it. This is the part people read."
	case utf8.RuneCountInString(out.Note) > MaxNoteRunes:
		errs["note"] = "That is a long reason. Trim it a little."
	}

	if utf8.RuneCountInString(out.Attribution) > MaxAttributionRunes {
		errs["attribution"] = "That is too long for a signature."
	}

	if len(errs) == 0 {
		return out, nil
	}
	return out, errs
}

// validateLink accepts only an absolute http or https URL with a host.
//
// Nothing here fetches the URL, now or ever: an outbound request per
// stranger submission is an SSRF surface and a way to make the box a
// request amplifier. The scheme check is also what keeps javascript: and
// data: URLs out of an href.
func validateLink(payload string, errs FieldErrors) {
	if payload == "" {
		errs["payload"] = "Add the link you want to leave."
		return
	}
	u, err := url.Parse(payload)
	if err != nil {
		errs["payload"] = "That does not look like a web address."
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		errs["payload"] = "Links need to start with http:// or https://"
		return
	}
	if u.Host == "" {
		errs["payload"] = "That link has no site in it."
	}
}

// LinkDomain is what the shelf shows for a link: the host, without a port
// and without a leading www.
//
// The shelf shows a domain rather than a title because fetching a title
// means an outbound request for every URL a stranger submits, and because a
// hostile URL cannot then borrow a trustworthy title.
func (it Item) LinkDomain() string {
	u, err := url.Parse(it.Payload)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}
