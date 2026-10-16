package core

import (
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
