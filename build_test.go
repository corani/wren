package wren_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRelease(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required for release-script tests: %v", tool, err)
		}
	}
	script, err := os.ReadFile("build.sh")
	require.NoError(t, err)

	for _, tt := range []struct {
		name       string
		args       []string
		setup      string
		tag        string
		wantError  string
		wantTag    bool
		wantRemote bool
	}{
		{name: "release", args: []string{"release", "v1.2.3"}, wantTag: true, wantRemote: true},
		{name: "short alias and prerelease", args: []string{"-r", "v1.2.3-rc.1"}, tag: "v1.2.3-rc.1", wantTag: true, wantRemote: true},
		{name: "initial automatic version", args: []string{"release"}, tag: "v0.0.1", wantTag: true, wantRemote: true},
		{name: "automatic version short alias", args: []string{"-r"}, tag: "v0.0.1", wantTag: true, wantRemote: true},
		{name: "numeric patch order", args: []string{"release"}, setup: "local versions", tag: "v1.2.11", wantTag: true, wantRemote: true},
		{name: "fetch remote stable versions", args: []string{"release"}, setup: "remote versions", tag: "v1.3.5", wantTag: true, wantRemote: true},
		{name: "ignore prereleases without stable versions", args: []string{"release"}, setup: "prerelease only", tag: "v0.0.1", wantTag: true, wantRemote: true},
		{name: "explicit version overrides automatic choice", setup: "local versions", tag: "v3.0.0", wantTag: true, wantRemote: true},
		{name: "invalid version", args: []string{"release", "1.2.3"}, wantError: "Expected vMAJOR"},
		{name: "trailing action", args: []string{"release", "v1.2.3", "test"}, wantError: "must be the final action"},
		{name: "tracked changes", setup: "tracked", wantError: "Commit or stash"},
		{name: "untracked changes", setup: "untracked", wantError: "Commit or stash"},
		{name: "detached HEAD", setup: "detached", wantError: "detached HEAD"},
		{name: "existing local tag", setup: "local tag", wantError: "already exists locally", wantTag: true},
		{name: "existing remote tag", setup: "remote tag", wantError: "already exists on origin", wantRemote: true},
		{name: "validation failure", setup: "failed checks", wantError: "return code 1"},
		{name: "earlier action failure", args: []string{"test", "release", "v1.2.3"}, setup: "failed checks", wantError: "after a failed action"},
		{name: "atomic push failure", setup: "rejected push", wantError: "local tag v1.2.3 remains", wantTag: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, origin, tools := t.TempDir(), t.TempDir(), t.TempDir()
			env := append(os.Environ(),
				"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
				"GIT_TERMINAL_PROMPT=0",
			)
			run := func(name string, args ...string) string {
				t.Helper()
				cmd := exec.Command(name, args...)
				cmd.Dir, cmd.Env = repo, env
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s %v: %s", name, args, output)
				return string(output)
			}
			git := func(args ...string) string {
				t.Helper()
				return run("git", args...)
			}
			require.NoError(t, os.WriteFile(filepath.Join(tools, "go"), []byte("#!/bin/sh\nexit \"${WREN_TEST_GO_EXIT:-0}\"\n"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(repo, "build.sh"), script, 0o755))
			git("init", "--bare", origin)
			git("init", "-b", "master")
			git("config", "user.name", "Release test")
			git("config", "user.email", "release-test@example.invalid")
			git("add", "build.sh")
			git("commit", "-m", "Initial commit")
			git("remote", "add", "origin", origin)
			git("push", "origin", "master")
			initial := git("rev-parse", "HEAD")
			require.NoError(t, os.WriteFile(filepath.Join(repo, "release.txt"), []byte("Release content\n"), 0o644))
			git("add", "release.txt")
			git("commit", "-m", "Release content")
			head := git("rev-parse", "HEAD")
			tag := "v1.2.3"
			if tt.tag != "" {
				tag = tt.tag
			}

			switch tt.setup {
			case "tracked":
				require.NoError(t, os.WriteFile(filepath.Join(repo, "release.txt"), []byte("Uncommitted\n"), 0o644))
			case "untracked":
				require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), nil, 0o644))
			case "detached":
				git("checkout", "--detach")
			case "local versions":
				for _, version := range []string{"v1.2.9", "v1.2.10", "v2.0.0-rc.1", "v99.00.1", "not-a-version"} {
					git("tag", version)
				}
			case "remote versions":
				git("tag", "v1.2.9")
				for _, version := range []string{"v1.3.4", "v2.0.0-rc.1"} {
					git("tag", version)
					git("push", "origin", "refs/tags/"+version)
					git("tag", "-d", version)
				}
			case "prerelease only":
				git("tag", "v1.0.0-rc.1")
			case "local tag", "remote tag":
				git("tag", "-a", tag, "-m", "Existing release")
				if tt.setup == "remote tag" {
					git("push", "origin", "refs/tags/"+tag)
					git("tag", "-d", tag)
				}
			case "failed checks":
				env = append(env, "WREN_TEST_GO_EXIT=1")
			case "rejected push":
				require.NoError(t, os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
			}

			args := tt.args
			if args == nil {
				args = []string{"release", tag}
			}
			cmd := exec.Command("bash", append([]string{"./build.sh"}, args...)...)
			cmd.Dir, cmd.Env = repo, env
			output, err := cmd.CombinedOutput()
			if tt.wantError != "" {
				require.Error(t, err, "%s", output)
				assert.Contains(t, string(output), tt.wantError)
			} else {
				require.NoError(t, err, "%s", output)
			}
			assert.Equal(t, tt.wantTag, git("tag", "--list", tag) != "")
			assert.Equal(t, tt.wantRemote, git("ls-remote", "--tags", "origin", "refs/tags/"+tag) != "")
			if tt.wantError == "" {
				assert.Equal(t, "tag\n", git("cat-file", "-t", "refs/tags/"+tag))
				assert.Equal(t, head, git("rev-parse", tag+"^{}"))
				assert.Equal(t, head, git("--git-dir="+origin, "rev-parse", "refs/heads/master"))
			} else {
				assert.Equal(t, initial, git("--git-dir="+origin, "rev-parse", "refs/heads/master"))
			}
		})
	}
}
