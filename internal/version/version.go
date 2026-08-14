// Package version carries build metadata stamped in at link time.
//
// AGPL-3.0 places the source-offer obligation on whoever runs a modified
// build. DESIGN.md §2 answers that with compliance by construction: the
// binary always knows its own version, commit, and repository.
package version

const (
	defaultVersion = "dev"
	defaultCommit  = "unknown"
	defaultRepoURL = "https://github.com/gumptionthomas/boulevard"
)

// Overridden at build time with -ldflags "-X ...".
var (
	Version = defaultVersion
	Commit  = defaultCommit
	RepoURL = defaultRepoURL
)

// String renders the metadata for `boulevard version` and the booklet cover footer.
func String() string {
	return "boulevard " + Version + " (" + Commit + ")\n" + RepoURL
}
