package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeHome points the user's home at a temporary folder with an agent's skills, a plugin, the project's memory and
// a skill's credentials folder, the way a developer's machine has them.
func fakeHome(t *testing.T, projectDir string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	for _, f := range []string{
		".claude/skills/nexscope-infra/SKILL.md", ".claude/plugins/installed.json", ".claude/CLAUDE.md",
		".claude/projects/" + claudeProjectKey(projectDir) + "/memory/MEMORY.md",
		".codex/skills/nexscope-infra/SKILL.md", ".codex/AGENTS.md", ".agents/skills/shared/SKILL.md",
		".pi/agent/private/nexscope-infra-access.json",
	} {
		p := filepath.Join(home, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte("x"), 0600)
	}
	return home
}

func setMaxSandbox(t *testing.T, a *App, level string) {
	t.Helper()
	c := a.Settings()
	c.MaxSandbox = level
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
}

func settingsArg(t *testing.T, args []any) map[string]any {
	t.Helper()
	for i, a := range args {
		if a == "--settings" && i+1 < len(args) {
			var m map[string]any
			if err := json.Unmarshal([]byte(args[i+1].(string)), &m); err != nil {
				t.Fatalf("settings are not JSON: %v", args[i+1])
			}
			return m
		}
	}
	return nil
}

func allowRules(settings map[string]any) []string {
	perms, _ := settings["permissions"].(map[string]any)
	var out []string
	for _, r := range perms["allow"].([]any) {
		out = append(out, r.(string))
	}
	return out
}

// Agents use their own skills, plugins and memory locally; a Loop must reach them too, with the Loop's read paths.
func TestAgentContextReachesTheRun(t *testing.T) {
	project := t.TempDir()
	home := fakeHome(t, project)
	for _, sandbox := range []string{"read-only", "workspace-write"} {
		t.Run(sandbox, func(t *testing.T) {
			a := testApp(t)
			log := useFakeAgents(t, a, "done")
			t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
			a.DetectAgents()
			setMaxSandbox(t, a, "workspace-write")
			l := agentLoop(t, "claude")
			l.Directory = project
			l.Sandbox = sandbox
			l.ReadPaths = []string{"~/.pi/agent/private"}
			l, err := a.SaveLoop(l)
			if err != nil {
				t.Fatal(err)
			}
			r, _ := a.Start(l.ID)
			waitStatus(t, a, r.ID, "completed")
			call := fakeCalls(t, log)[0]
			args := call["args"].([]any)
			settings := settingsArg(t, args)
			if settings == nil {
				t.Fatalf("no --settings: %v", args)
			}
			rules := allowRules(settings)
			for _, want := range []string{
				claudeReadRule(filepath.Join(home, ".claude", "skills"), true),
				claudeReadRule(filepath.Join(home, ".claude", "plugins"), true),
				claudeReadRule(filepath.Join(home, ".claude", "CLAUDE.md"), false),
				claudeReadRule(filepath.Join(home, ".claude", "projects", claudeProjectKey(project), "memory"), true),
				claudeReadRule(filepath.Join(home, ".agents", "skills"), true),
				claudeReadRule(filepath.Join(home, ".pi", "agent", "private"), true),
			} {
				if !slices.Contains(rules, want) {
					t.Errorf("missing %s in %v", want, rules)
				}
			}
			for _, r := range rules {
				if strings.Contains(r, ".codex") || strings.Contains(r, "Edit(") || strings.Contains(r, "Write(") {
					t.Errorf("unexpected rule %s", r)
				}
			}
			if sandbox == "read-only" && !strings.Contains(strings.Join(toStrings(args), " "), "--tools Read,Grep,Glob,Skill") {
				t.Errorf("read-only Claude cannot load skills: %v", args)
			}
			if _, ok := settings["sandbox"]; ok != (sandbox == "workspace-write") {
				t.Errorf("sandbox settings at %s: %v", sandbox, settings)
			}
			prompt := call["prompt"].(string)
			if !strings.Contains(prompt, filepath.Join(home, ".pi", "agent", "private")) {
				t.Errorf("prompt does not name the read path: %s", prompt)
			}
		})
	}
}

// Codex's sandboxes read the whole disk; it gets no extra flags, but the prompt still names the read paths. In a
// worktree the agent would not find the project's memory by itself, so the prompt points at it.
func TestCodexAndWorktreeGetReadPathsAndMemory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := gitRepo(t, map[string]string{"a.txt": "a"})
	home := fakeHome(t, repo)
	a := testApp(t)
	log := useFakeAgents(t, a, "done")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	a.DetectAgents()
	l := agentLoop(t, "claude")
	l.Directory = repo
	l.Workspace = "worktree"
	l.ReadPaths = []string{filepath.Join(home, ".pi", "agent", "private")}
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	prompt := fakeCalls(t, log)[0]["prompt"].(string)
	memory := filepath.Join(home, ".claude", "projects", claudeProjectKey(repo), "memory")
	if !strings.Contains(prompt, memory) {
		t.Fatalf("worktree prompt does not point at the project's memory: %s", prompt)
	}

	b := testApp(t)
	log = useFakeAgents(t, b, "done")
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	b.DetectAgents()
	c := agentLoop(t, "codex")
	c.ReadPaths = []string{"~/.pi/agent/private"}
	c, err = b.SaveLoop(c)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = b.Start(c.ID)
	waitStatus(t, b, r.ID, "completed")
	call := fakeCalls(t, log)[0]
	if args := strings.Join(toStrings(call["args"].([]any)), " "); strings.Contains(args, "--add-dir") || strings.Contains(args, "--settings") {
		t.Fatalf("codex got write access or Claude settings for reading: %s", args)
	}
	if !strings.Contains(call["prompt"].(string), filepath.Join(home, ".pi", "agent", "private")) {
		t.Fatal("codex prompt does not name the read path")
	}

	// A read path that does not exist on this computer blocks the Run and says how to fix it.
	c.ReadPaths = []string{"~/.pi/agent/elsewhere"}
	if c, err = b.SaveLoop(c); err != nil {
		t.Fatal(err)
	}
	r, _ = b.Start(c.ID)
	waitStatus(t, b, r.ID, "blocked")
	if r, _ = b.Run(r.ID); !strings.Contains(r.Error, "does not exist on this computer") {
		t.Fatal(r.Error)
	}
}

func TestReadPathAndCopyFileValidation(t *testing.T) {
	home := fakeHome(t, t.TempDir())
	l := testLoop(t)
	for _, ok := range []string{"~/.pi/agent/private", filepath.Join(home, "notes"), "~/.config/nexscope"} {
		l.ReadPaths = []string{ok}
		if err := Validate(l); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"~", "~/", home, "relative/dir", "~/.ssh", "~/.ssh/id_ed25519", "~/.aws", "~/.config/gh", "~/../x", "/", " ~/x", filepath.Dir(home)} {
		l.ReadPaths = []string{bad}
		if Validate(l) == nil {
			t.Errorf("read path %q accepted", bad)
		}
	}
	l.ReadPaths = nil
	l.CopyFiles = []string{".env"}
	if err := Validate(l); err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("copied files outside a worktree: %v", err)
	}
	l.Workspace = "worktree"
	for _, ok := range []string{".env", "CLAUDE.local.md", "config/local.yml", ".claude/settings.local.json"} {
		l.CopyFiles = []string{ok}
		if err := Validate(l); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../other/.env", "a/../../b", ".git/config", "~/x", ".", ""} {
		l.CopyFiles = []string{bad}
		if Validate(l) == nil {
			t.Errorf("copied file %q accepted", bad)
		}
	}
}

func TestClaudeReadRules(t *testing.T) {
	if runtime.GOOS == "windows" {
		if got := claudeReadRule(`C:\Users\Administrator\.pi\agent\private`, true); got != "Read(//c/Users/Administrator/.pi/agent/private/**)" {
			t.Fatal(got)
		}
		return
	}
	if got := claudeReadRule("/Users/lee/.pi/agent/private", true); got != "Read(//Users/lee/.pi/agent/private/**)" {
		t.Fatal(got)
	}
	if got := claudeReadRule("/Users/lee/.claude/CLAUDE.md", false); got != "Read(//Users/lee/.claude/CLAUDE.md)" {
		t.Fatal(got)
	}
	if claudeProjectKey("/Users/lee/workspaces/loopbee") != "-Users-lee-workspaces-loopbee" {
		t.Fatal(claudeProjectKey("/Users/lee/workspaces/loopbee"))
	}
}

// gitRepo creates a committed repository with the given files.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for name, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte(body), 0600)
	}
	runGit(t, repo, "init", "-q", "-b", "master")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

func toStrings(v []any) []string {
	out := make([]string, len(v))
	for i, x := range v {
		out[i], _ = x.(string)
	}
	return out
}

// A fresh worktree has none of the project's ignored files (.env, CLAUDE.local.md). loopbee copies the listed ones
// and the agents' local files, and never commits them, even when the agent un-ignores and changes them.
func TestWorktreeCopiesIgnoredFilesAndNeverCommitsThem(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := gitRepo(t, map[string]string{".gitignore": ".env\nCLAUDE.local.md\nconfig/local/\n", "a.txt": "a"})
	os.WriteFile(filepath.Join(repo, ".env"), []byte("API_KEY=local\n"), 0600)
	os.WriteFile(filepath.Join(repo, "CLAUDE.local.md"), []byte("my notes\n"), 0600)
	os.MkdirAll(filepath.Join(repo, "config", "local"), 0700)
	os.WriteFile(filepath.Join(repo, "config", "local", "db.yml"), []byte("db: test\n"), 0600)
	a := testApp(t)
	useFakeAgents(t, a, "unignore")
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	a.DetectAgents()
	setMaxSandbox(t, a, "workspace-write")
	l := agentLoop(t, "codex")
	l.Sandbox = "workspace-write"
	l.Directory = repo
	l.Workspace = "worktree"
	l.Branch = "loopbee/env-{run}"
	l.CopyFiles = []string{".env", "config/local", "a.txt"} // a.txt is tracked: already in the worktree, skipped
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	r, _ = a.Run(r.ID)
	if b, err := os.ReadFile(filepath.Join(r.Workdir, "config", "local", "db.yml")); err != nil || string(b) != "db: test\n" {
		t.Fatalf("copied folder: %q %v", b, err)
	}
	files := runGit(t, repo, "show", "--name-only", "--format=", r.Branch)
	if !strings.Contains(files, "seen.txt") || !strings.Contains(files, ".gitignore") {
		t.Fatalf("the agent's own changes were not committed: %q", files)
	}
	if strings.Contains(files, ".env") || strings.Contains(files, "CLAUDE.local.md") || strings.Contains(files, "config/local") {
		t.Fatalf("copied files were committed: %q", files)
	}
	if seen := runGit(t, repo, "show", r.Branch+":seen.txt"); seen != "API_KEY=local\nmy notes" {
		t.Fatalf("agent saw %q", seen)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".env")); string(b) != "API_KEY=local\n" {
		t.Fatal("the developer's .env changed")
	}

	// A listed file that Git does not ignore would be committed: refused with the fix.
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("x"), 0600)
	l.CopyFiles = []string{"notes.txt"}
	if l, err = a.SaveLoop(l); err != nil {
		t.Fatal(err)
	}
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "blocked")
	if r, _ = a.Run(r.ID); !strings.Contains(r.Error, "not ignored by Git") || !strings.Contains(r.Error, ".gitignore") {
		t.Fatal(r.Error)
	}
	l.CopyFiles = []string{"missing.env"}
	if l, err = a.SaveLoop(l); err != nil {
		t.Fatal(err)
	}
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "blocked")
	if r, _ = a.Run(r.ID); !strings.Contains(r.Error, "does not exist") {
		t.Fatal(r.Error)
	}
}

// The product name became lowercase, but secrets saved before stay under the old keychain service; macOS matches it
// case-sensitively, so renaming it would make every saved secret unreadable.
func TestKeychainServiceSurvivesTheRename(t *testing.T) {
	if keyringService != "Loopbee" {
		t.Fatalf("keychain service is %q; secrets saved by earlier versions are stored under \"Loopbee\"", keyringService)
	}
}

// Apps started from the Dock or at login do not see what the shell profile exports; env: references ask the login
// shell, and a missing variable says where to set it.
func TestEnvReferenceFallsBackToLoginShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reads the saved user variables instead (userenv_windows.go)")
	}
	dir := t.TempDir()
	shell := filepath.Join(dir, "fakesh")
	os.WriteFile(shell, []byte("#!/bin/sh\necho 'motd noise'\nprintf '\\n__LOOPBEE_ENV__from-profile\\n'\n"), 0700)
	t.Setenv("SHELL", shell)
	os.Unsetenv("LOOPBEE_ONLY_IN_PROFILE")
	if v, err := resolveSecret("env:LOOPBEE_ONLY_IN_PROFILE"); err != nil || v != "from-profile" {
		t.Fatalf("%q %v", v, err)
	}
	t.Setenv("LOOPBEE_ONLY_IN_PROFILE", "from-app")
	if v, _ := resolveSecret("env:LOOPBEE_ONLY_IN_PROFILE"); v != "from-app" {
		t.Fatal("loopbee's own environment must win")
	}
	os.WriteFile(shell, []byte("#!/bin/sh\nprintf '\\n__LOOPBEE_ENV__\\n'\n"), 0700)
	_, err := resolveSecret("env:LOOPBEE_NOWHERE")
	if err == nil || !strings.Contains(err.Error(), "login shell") || strings.Contains(err.Error(), "notification") {
		t.Fatalf("missing variable: %v", err)
	}
	if _, ok := userEnv("BAD;NAME"); ok {
		t.Fatal("names are not passed to the shell unchecked")
	}
}

// A blocked agent says what it could not get; the Run, its notification and the next person see the list.
func TestResultListsWhatWasMissing(t *testing.T) {
	r := parseResult(resultMarker + ` {"status":"blocked","summary":"x","missing":["a","a","","` + strings.Repeat("y", 300) + `","b\nsecond line","c","d","e","f","g","h","i","j","k"]}`)
	if r == nil || len(r.Missing) != 10 || r.Missing[0] != "a" || len([]rune(r.Missing[1])) > 201 || r.Missing[2] != "b" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(resultInstructions("en"), `"missing"`) {
		t.Fatal("the result contract does not ask for missing items")
	}
	a := testApp(t)
	useFakeAgents(t, a, "missing")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	a.DetectAgents()
	l, err := a.SaveLoop(agentLoop(t, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	run, _ := a.Start(l.ID)
	waitStatus(t, a, run.ID, "blocked")
	run, _ = a.Run(run.ID)
	if run.Result == nil || len(run.Result.Missing) != 2 || !strings.Contains(run.Result.Missing[0], "nexscope.json") {
		t.Fatalf("%+v", run.Result)
	}
	if text := runNotification(run, "zh-TW", false); !strings.Contains(text, "代理拿不到: ~/.pi/agent/private/nexscope.json (EPERM); network access to api.example.com") {
		t.Fatal(text)
	}
}

// Claude Code's own advice (set sandbox.failIfUnavailable=false) cannot work under loopbee; the error says what does.
func TestClaudeSandboxUnavailableSaysHowToFix(t *testing.T) {
	reason := "Sandbox required but unavailable: sandbox is enabled but the Windows sandbox is not active on this session (feature gate off)."
	if got := explainFailure("claude", reason, "windows"); !strings.Contains(got, "Switch this Loop to Codex") || !strings.Contains(got, "agent-config") || !strings.HasPrefix(got, reason) {
		t.Fatal(got)
	}
	if got := explainFailure("claude", "Sandbox required but unavailable: bubblewrap (bwrap) not installed", "linux"); !strings.Contains(got, "bubblewrap and socat") {
		t.Fatal(got)
	}
	if got := explainFailure("codex", "boom", "windows"); got != "boom" {
		t.Fatal(got)
	}
	a := testApp(t)
	useFakeAgents(t, a, "nosandbox")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	a.DetectAgents()
	setMaxSandbox(t, a, "workspace-write")
	l := agentLoop(t, "claude")
	l.Sandbox = "workspace-write"
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "failed")
	r, _ = a.Run(r.ID)
	if !strings.Contains(r.Error, "Sandbox required but unavailable") || !strings.Contains(r.Error, "has no effect: loopbee sets it") {
		t.Fatal(r.Error)
	}
}

// The designer knows what every run has (skills, plugins, memory) and how to give a Loop more (read paths, copied
// files, shell variables); proposals may reference shell variables but never carry values.
func TestAuthorKnowsTheAgentContext(t *testing.T) {
	req := AuthorRequest{Messages: []ChatMessage{{Role: "user", Text: "daily"}}}
	p := authorPrompt(req, "/x", "en", "UTC", "claude", "workspace-write", Loop{})
	for _, want := range []string{"skills, plugins, subagents", "readPaths", "copyFiles", `"env:NAME"`, "This computer runs " + platformName(), "Never ~/.ssh"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if !strings.Contains(designerContext([]string{"/h/.claude/skills"}), "/h/.claude/skills") || designerContext(nil) != "" {
		t.Fatal("designer context")
	}
	base := Loop{Env: []EnvVar{{Name: "STORED", Value: "keyring:x", Secret: true}}}
	p2 := normalizeDraft(Loop{Workspace: "direct", CopyFiles: []string{".env"}, Env: []EnvVar{
		{Name: "AWS_PROFILE", Value: "env:AWS_PROFILE"}, {Name: "TOKEN", Value: "literal-secret", Secret: true},
		{Name: "STORED", Value: "env:STORED", Secret: true}, {Name: "ODD", Value: "env:bad name"}}}, base, "codex")
	got := map[string]string{}
	for _, e := range p2.Env {
		got[e.Name] = e.Value
	}
	if got["AWS_PROFILE"] != "env:AWS_PROFILE" || got["TOKEN"] != "" || got["STORED"] != "keyring:x" || got["ODD"] != "" {
		t.Fatalf("%v", got)
	}
	if p2.CopyFiles != nil {
		t.Fatal("copied files kept outside a worktree")
	}
	def := definitionForAgent(Loop{Env: []EnvVar{{Name: "A", Value: "keyring:secret"}, {Name: "B", Value: "env:B"}}})
	env := def["env"].([]map[string]any)
	if env[0]["value"] != "" || env[1]["value"] != "env:B" {
		t.Fatalf("stored values shown to the agent: %v", env)
	}
}

// Deleting a Loop removed only its definition: its worktree stayed registered in the developer's repository and its
// secrets stayed in the keychain. Now both go; the branch (possibly unreviewed work) and the Runs stay.
func TestDeleteLoopCleansUp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	var deleted []string
	orig := deleteSecret
	deleteSecret = func(ref string) error { deleted = append(deleted, ref); return nil }
	t.Cleanup(func() { deleteSecret = orig })
	repo := gitRepo(t, map[string]string{"a.txt": "a"})
	a := testApp(t)
	useFakeAgents(t, a, "edit")
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	a.DetectAgents()
	l := agentLoop(t, "codex")
	l.Directory = repo
	l.Workspace = "worktree"
	l.Branch = "loopbee/keep-{run}"
	l.Env = append(l.Env, EnvVar{Name: "TOKEN", Value: "keyring:pub/loop/abc/TOKEN", Secret: true}, EnvVar{Name: "SHARED", Value: "keyring:pub/telegram/secret", Secret: true})
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	// Prepare the worktree the way a Run does (a real Run would also need the keychain values).
	if _, err := a.prepareWorkspace(t.Context(), l, "r1", 0); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(a.Dir, "worktrees", l.ID)
	if !strings.Contains(runGit(t, repo, "worktree", "list"), l.ID) {
		t.Fatal("worktree not registered")
	}
	if err := a.DeleteLoop(l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree folder left behind")
	}
	if strings.Contains(runGit(t, repo, "worktree", "list"), l.ID) {
		t.Fatal("worktree still registered in the developer's repository")
	}
	if !strings.Contains(runGit(t, repo, "branch", "--list", "loopbee/keep-*"), "loopbee/keep-") {
		t.Fatal("the branch with the agent's work was deleted")
	}
	if len(deleted) != 1 || deleted[0] != "pub/loop/abc/TOKEN" {
		t.Fatalf("keychain entries removed: %v", deleted)
	}
}
