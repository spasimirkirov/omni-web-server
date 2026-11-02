package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidGitURL(t *testing.T) {
	good := []string{
		"https://github.com/user/repo.git", "http://example.com/a/b", "git@github.com:user/repo.git",
		"ssh://git@host.example:2222/user/repo.git", "file:///D:/repos/x.git", "file:///home/me/repo",
		"https://user:token@gitlab.com/g/p.git", "deploy_key@git.example.org:team/app",
	}
	for _, u := range good {
		if err := validGitURL(u); err != nil {
			t.Errorf("%q should be valid: %v", u, err)
		}
	}
	bad := []string{
		"", "-u", "--upload-pack=calc", "https://github.com/a b.git", "https://github.com/a\n.git", "https://github.com/a\r/b",
		"ftp://example.com/x.git", "git://example.com/x.git", "ext::sh -c calc", "ssh://-oProxyCommand=calc/x",
		"ssh://-oProxyCommand=calc@host/x", "https:///path", "https://host", "file://", "github.com/user/repo", `C:\repos\x`,
		"-oProxyCommand=x@host:path", "git@-host:path", "git@host:", "javascript:alert(1)",
	}
	for _, u := range bad {
		if err := validGitURL(u); err == nil {
			t.Errorf("%q should be rejected", u)
		}
	}
}

func TestValidGitRef(t *testing.T) {
	for _, r := range []string{"", "main", "v1.2.3", "feature/login-form", "release-2.0", "user@fix"} {
		if err := validGitRef(r); err != nil {
			t.Errorf("%q should be valid: %v", r, err)
		}
	}
	for _, r := range []string{"-main", "--upload-pack=x", "a b", "a..b", "feat/", "x.lock", "a\nb", "a;b", "$(x)"} {
		if err := validGitRef(r); err == nil {
			t.Errorf("%q should be rejected", r)
		}
	}
}

func TestGitRepoName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/user/repo.git":     "repo",
		"https://github.com/user/repo":         "repo",
		"https://github.com/user/repo.git/":    "repo",
		"git@github.com:user/my-app.git":       "my-app",
		"git@github.com:repo.git":              "repo",
		"ssh://git@host:22/team/Blog_Site.git": "Blog_Site",
		"file:///D:/repos/shop.git":            "shop",
		"https://github.com/user/a.b.git?x=1":  "a.b",
		"https://github.com/user/we ird!.git":  "we-ird",
		"https://github.com/user/.git":         "",
		"":                                     "",
	}
	for in, want := range cases {
		if got := gitRepoName(in); got != want {
			t.Errorf("gitRepoName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := gitRepoName("https://x.com/u/" + strings.Repeat("a", 100)); len(got) != 63 {
		t.Errorf("long names are cut to 63 characters, got %d", len(got))
	}
}

func TestGitCloneArgs(t *testing.T) {
	got := gitCloneArgs("https://x.com/u/r.git", "", false, "D:/www/.omni-new-r")
	want := []string{"-c", "core.longpaths=true", "clone", "--", "https://x.com/u/r.git", "D:/www/.omni-new-r"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plain clone: %v", got)
	}
	got = gitCloneArgs("git@h:u/r.git", "v1.0", true, "t")
	want = []string{"-c", "core.longpaths=true", "clone", "--depth", "1", "--branch", "v1.0", "--", "git@h:u/r.git", "t"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shallow clone with branch: %v", got)
	}
}

func TestRedactGitURL(t *testing.T) {
	if got := redactGitURL("https://u:secret@h.com/r.git"); strings.Contains(got, "secret") {
		t.Errorf("password leaked: %s", got)
	}
	for _, u := range []string{"https://h.com/r.git", "git@h.com:u/r.git"} {
		if redactGitURL(u) != u {
			t.Errorf("%s should stay as it is", u)
		}
	}
}

func TestCloneFailureHint(t *testing.T) {
	err := cloneFailure(os.ErrNotExist)
	if strings.Contains(err.Error(), "private repo") {
		t.Errorf("no hint expected: %v", err)
	}
	err = cloneFailure(&testErr{"git clone failed: fatal: could not read Username for 'https://github.com': terminal prompts disabled"})
	if !strings.Contains(err.Error(), "private repo: set up a credential helper or SSH key") {
		t.Errorf("hint missing: %v", err)
	}
}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

// TestGitCloneLocal clones a throwaway local repository with the real git
// binary and the argument list the app builds. No network is used.
func TestGitCloneLocal(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	src := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "index.php"), []byte("<?php echo 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-q", "-m", "first")
	url := "file:///" + strings.TrimPrefix(filepath.ToSlash(src), "/")
	if err := validGitURL(url); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "clone")
	run(filepath.Dir(dest), gitCloneArgs(url, "main", true, dest)...)
	if !exists(filepath.Join(dest, "index.php")) {
		t.Error("index.php was not cloned")
	}
	if err := removeAllForce(dest); err != nil || exists(dest) {
		t.Errorf("removeAllForce left %s: %v", dest, err)
	}
}
