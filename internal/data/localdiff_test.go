package data

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalDiffFiles(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name, s string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644))
	}
	run("init", "-q")
	write("a.txt", "1\n2\n3\n")
	write("gone.txt", "x\n")
	run("add", ".")
	run("commit", "-qm", "init")
	write("a.txt", "1\ntwo\n3\n4\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	write("new.txt", "hello\n")

	files, err := LocalDiffFiles(dir, "HEAD")
	require.NoError(t, err)
	byName := map[string]PRFile{}
	for _, f := range files {
		byName[f.Filename] = f
	}
	require.Len(t, files, 3)
	require.Equal(t, "modified", byName["a.txt"].Status)
	require.Equal(t, 2, byName["a.txt"].Additions)
	require.Equal(t, 1, byName["a.txt"].Deletions)
	require.Contains(t, byName["a.txt"].Patch, "@@ -1,3 +1,4 @@")
	require.Equal(t, "removed", byName["gone.txt"].Status)
	require.Equal(t, "added", byName["new.txt"].Status)
	require.Equal(t, "@@ -0,0 +1 @@\n+hello", byName["new.txt"].Patch)
}
