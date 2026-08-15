package boulevard

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// EntropyBytes is 128 bits, per DESIGN.md §4.
const EntropyBytes = 16

// LibraryID is a distinct type so the compiler enforces DESIGN.md §10:
// every repository method takes one, and no ambient "current library"
// exists anywhere in the codebase.
//
// It is deliberately not the slug. Slugs are renameable; an ID must be
// stable and unique across a host so a database file can be copied out
// and stand alone.
type LibraryID string

func NewLibraryID(r io.Reader) (LibraryID, error) {
	s, err := RandomBase32(r, EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new library id: %w", err)
	}
	return LibraryID(s), nil
}

type Library struct {
	ID            LibraryID
	Slug          string
	Name          string
	LocationLabel string
	BaseURL       string

	// The §3 settings. Defaults live in migration 2, not here, so a row
	// created by any path gets them.
	Slots            int
	MaxAgeDays       int
	DefaultCopies    int
	ApprovalRequired bool
	StewardContact   string
}

// Slugify lowercases, replaces runs of non-alphanumerics with a single
// hyphen, and trims hyphens from the ends.
func Slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ValidateBaseURL enforces scheme + host and nothing else. Every token card
// and the permanent browse sign encode this value, so a path or query here
// becomes twelve dead cards and a wrong permanent sign (DESIGN.md §8).
//
// It returns the normalized base URL and the bare hostname it parsed out.
// Handing back the host means callers doing a DNS lookup need not parse the
// URL a second time to recover something already known here.
//
// The host is lowercased. Hostnames are case-insensitive, but the stored
// string is compared literally on every re-run: without this,
// https://Example.org followed by https://example.org reads as a base-URL
// change, fires the loudest warning the tool has, and rewrites every QR
// payload for nothing.
func ValidateBaseURL(raw string) (normalized, host string, err error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", errors.New("base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse base URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("base URL must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", "", errors.New("base URL must include a host")
	}
	if u.User != nil {
		return "", "", errors.New("base URL must not include credentials")
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return "", "", fmt.Errorf("base URL must not include a path, got %q", u.Path)
	}
	if u.RawQuery != "" {
		return "", "", errors.New("base URL must not include a query string")
	}
	if u.Fragment != "" {
		return "", "", errors.New("base URL must not include a fragment")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), strings.ToLower(u.Hostname()), nil
}
