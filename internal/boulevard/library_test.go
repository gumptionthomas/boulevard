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
	tests := []struct{ in, want string }{
		{"https://boulevard.example.org", "https://boulevard.example.org"},
		{"https://boulevard.example.org/", "https://boulevard.example.org"},
		{"http://localhost:8080", "http://localhost:8080"},
	}
	for _, tc := range tests {
		got, err := ValidateBaseURL(tc.in)
		if err != nil {
			t.Errorf("ValidateBaseURL(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ValidateBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
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
		if _, err := ValidateBaseURL(in); err == nil {
			t.Errorf("ValidateBaseURL(%q) succeeded, want error", in)
		}
	}
}
