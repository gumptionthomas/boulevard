package boulevard

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Fairview Boulevard", "the-fairview-boulevard"},
		{"4th & Fairview", "4th-fairview"},
		{"  padded  ", "padded"},
		{"Ümlaut Box", "mlaut-box"},
		{"multiple---hyphens", "multiple-hyphens"},
	}
	for _, tc := range tests {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateBaseURLAccepts(t *testing.T) {
	tests := []struct{ in, want, wantHost string }{
		{"https://boulevard.example.org", "https://boulevard.example.org", "boulevard.example.org"},
		{"https://boulevard.example.org/", "https://boulevard.example.org", "boulevard.example.org"},
		{"http://localhost:8080", "http://localhost:8080", "localhost"},
	}
	for _, tc := range tests {
		got, host, err := ValidateBaseURL(tc.in)
		if err != nil {
			t.Errorf("ValidateBaseURL(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ValidateBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if host != tc.wantHost {
			t.Errorf("ValidateBaseURL(%q) host = %q, want %q", tc.in, host, tc.wantHost)
		}
	}
}

// A hostname is case-insensitive, but the stored base URL is compared
// literally on every re-run. Normalizing the case here is what keeps
// https://Example.org from reading as a base-URL change against a stored
// https://example.org — which would warn loudly and rewrite every QR.
func TestValidateBaseURLLowercasesTheHost(t *testing.T) {
	tests := []struct{ in, want, wantHost string }{
		{"https://Boulevard.Example.ORG", "https://boulevard.example.org", "boulevard.example.org"},
		{"HTTPS://Example.org", "https://example.org", "example.org"},
		{"http://LocalHost:8080", "http://localhost:8080", "localhost"},
	}
	for _, tc := range tests {
		got, host, err := ValidateBaseURL(tc.in)
		if err != nil {
			t.Errorf("ValidateBaseURL(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ValidateBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if host != tc.wantHost {
			t.Errorf("ValidateBaseURL(%q) host = %q, want %q", tc.in, host, tc.wantHost)
		}
	}
}

func TestValidateBaseURLRejects(t *testing.T) {
	// Every card and the permanent browse sign encode this host, so anything
	// that would produce a wrong or unstable URL must be refused (spec §9.1).
	for _, in := range []string{
		"",
		"boulevard.example.org",       // no scheme
		"ftp://boulevard.example.org", // wrong scheme
		"https://",                    // no host
		"https://example.org/shelf",   // path
		"https://example.org?a=1",     // query
		"https://example.org#frag",    // fragment
		"https://user:pw@example.org", // credentials
	} {
		if _, _, err := ValidateBaseURL(in); err == nil {
			t.Errorf("ValidateBaseURL(%q) succeeded, want error", in)
		}
	}
}
