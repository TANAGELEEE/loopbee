package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The person named the project folder in the chat but left the folder field empty: the draft had no working
// directory, so the agent put the folder into readPaths and the Loop could not be saved. With no folder chosen, the
// designer may propose one; loopbee keeps it only when it is an absolute folder that exists. A folder that was
// chosen always wins.
func TestChatDraftTakesTheFolderTheUserNamed(t *testing.T) {
	named := t.TempDir()
	if got := normalizeDraft(Loop{Directory: named}, Loop{}, "pi").Directory; got != named {
		t.Fatalf("named folder dropped: %q", got)
	}
	for _, bad := range []string{"relative/dir", filepath.Join(named, "missing"), ""} {
		if got := normalizeDraft(Loop{Directory: bad}, Loop{}, "pi").Directory; got != "" {
			t.Fatalf("proposed %q became %q", bad, got)
		}
	}
	chosen := t.TempDir()
	if got := normalizeDraft(Loop{Directory: named}, Loop{Directory: chosen}, "pi").Directory; got != chosen {
		t.Fatalf("the chosen folder must win, got %q", got)
	}
	if !strings.Contains(authorPrompt(AuthorRequest{}, "/tmp/scratch", "en", "UTC", "pi", "read-only", Loop{}), "loop.directory") {
		t.Fatal("without a folder the designer is not told it may propose one")
	}
	props := authorSchema()["properties"].(map[string]any)["loop"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["directory"]; !ok {
		t.Fatal("the answer schema has no loop.directory")
	}
}

// A folder that holds several repositories is not a Git repository itself: requiring a clean tree there blocked every
// Run. Drafts drop the requirement for such a folder, and the check says which problem it found.
func TestCleanTreeRequirementFitsTheFolder(t *testing.T) {
	plain, repo := t.TempDir(), t.TempDir()
	if out, err := runCommand(context.Background(), repo, []string{"git", "init", "-q"}, nil); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	base := Loop{RequireClean: true, Workspace: "direct"}
	if normalizeDraft(Loop{Workspace: "direct"}, Loop{Directory: plain, RequireClean: true}, "pi").RequireClean {
		t.Fatal("a folder that is not a Git repository kept “require clean tree”")
	}
	base.Directory = repo
	if !normalizeDraft(Loop{Workspace: "direct"}, base, "pi").RequireClean {
		t.Fatal("a Git repository lost “require clean tree”")
	}

	a := testApp(t)
	run := func(dir string) Run {
		l := testLoop(t)
		l.Directory, l.RequireClean = dir, true
		l, err := a.SaveLoop(l)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "blocked")
		r, _ = a.Run(r.ID)
		return r
	}
	if r := run(plain); !strings.Contains(r.Error, "not a Git repository") {
		t.Fatalf("non-Git folder: %q", r.Error)
	}
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0600)
	if r := run(repo); !strings.Contains(r.Error, "uncommitted changes") {
		t.Fatalf("dirty repository: %q", r.Error)
	}
}
