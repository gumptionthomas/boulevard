package version

import "testing"

func TestStringIncludesAllBuildMetadata(t *testing.T) {
	Version, Commit, RepoURL = "1.2.3", "abc1234", "https://example.org/repo"
	got := String()
	want := "boulevard 1.2.3 (abc1234)\nhttps://example.org/repo"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDefaultsAreNotEmpty(t *testing.T) {
	if defaultVersion == "" || defaultCommit == "" || defaultRepoURL == "" {
		t.Fatal("build metadata defaults must be non-empty so an un-stamped build still identifies itself")
	}
}
