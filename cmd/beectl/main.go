// beectl is the command-line companion of Loopbee. It talks to the running app over its loopback API
// using the launch token in <data>/launch-url.txt, so agents (Codex, Claude Code) can create draft loops,
// inspect runs and start loops from their own conversations.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"loopbee/internal/core"
)

//go:embed SKILL.md
var skill []byte

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "beectl:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: beectl [-data DIR] <command>

  status                         app, agents and active runs
  loops [--json]                 list loops
  runs [--loop ID|NAME] [--json] recent runs
  schema                         JSON schema of a loop definition with an example
  create -f FILE|-               create a disabled draft loop from JSON
  run ID|NAME                    start a run now
  skill print                    print the agent skill
  skill install [--agent codex|claude|all] [--dir DIR]
                                 install the skill for Codex and/or Claude Code
`)
}

func dataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("LOOPBEE_DATA_DIR"); v != "" {
		return v
	}
	home, _ := os.UserConfigDir()
	return filepath.Join(home, "loopbee")
}

type client struct {
	base string
	http *http.Client
}

func connect(dir string) (*client, error) {
	b, err := os.ReadFile(filepath.Join(dir, "launch-url.txt"))
	if err != nil {
		return nil, fmt.Errorf("Loopbee does not seem to be running (no launch-url.txt in %s); start the app first", dir)
	}
	url := strings.TrimSpace(string(b))
	base, frag, ok := strings.Cut(url, "/#")
	if !ok {
		return nil, errors.New("unexpected launch-url.txt format")
	}
	token := strings.TrimPrefix(frag, "token=")
	jar, _ := cookiejar.New(nil)
	c := &client{base: base, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	if err := c.do("POST", "/api/session", map[string]string{"token": token}, nil); err != nil {
		return nil, fmt.Errorf("cannot reach Loopbee at %s: %w", base, err)
	}
	return c, nil
}

func (c *client) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *client) state() (core.State, error) {
	var s core.State
	err := c.do("GET", "/api/state", nil, &s)
	return s, err
}

func findLoop(s core.State, key string) (core.Loop, error) {
	for _, l := range s.Loops {
		if l.ID == key || strings.EqualFold(l.Name, key) {
			return l, nil
		}
	}
	return core.Loop{}, fmt.Errorf("no loop with id or name %q", key)
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("beectl", flag.ContinueOnError)
	data := fs.String("data", "", "Loopbee data directory (default: LOOPBEE_DATA_DIR or the app default)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		usage(out)
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" {
		usage(out)
		return nil
	}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "schema":
		return printSchema(out)
	case "skill":
		return skillCommand(rest, out)
	}
	c, err := connect(dataDir(*data))
	if err != nil {
		return err
	}
	switch cmd {
	case "status":
		s, err := c.state()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Loopbee %s · %s/%s · data %s\n", s.Info.Version, s.Info.OS, s.Info.Arch, s.Info.DataDir)
		fmt.Fprintf(out, "default agent: %s · permission ceiling: %s · schedules paused: %v\n", orDash(s.Settings.DefaultAgent), s.Settings.MaxSandbox, s.Settings.Paused)
		for _, a := range s.Agents {
			fmt.Fprintf(out, "  %-7s %-11s %s\n", a.Name, a.Status, a.Version)
		}
		active := 0
		for _, r := range s.Runs {
			if r.Status == "running" || r.Status == "waiting" || r.Status == "queued" {
				active++
				fmt.Fprintf(out, "  active: %s %s (%s)\n", r.ID[:10], r.Definition.Name, r.Status)
			}
		}
		fmt.Fprintf(out, "%d loops, %d runs, %d active\n", len(s.Loops), len(s.Runs), active)
		return nil
	case "loops":
		s, err := c.state()
		if err != nil {
			return err
		}
		if hasFlag(rest, "--json") {
			return writeJSON(out, s.Loops)
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSCHEDULE\tENABLED\tLAST")
		for _, l := range s.Loops {
			last := "-"
			for _, r := range sortedRuns(s.Runs) {
				if r.LoopID == l.ID {
					last = r.Status + " " + r.Started.Format("01-02 15:04")
					break
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\n", l.ID, l.Name, orDash(l.Cron), l.Enabled, last)
		}
		return w.Flush()
	case "runs":
		s, err := c.state()
		if err != nil {
			return err
		}
		runs := sortedRuns(s.Runs)
		if key := flagValue(rest, "--loop"); key != "" {
			l, err := findLoop(s, key)
			if err != nil {
				return err
			}
			filtered := runs[:0]
			for _, r := range runs {
				if r.LoopID == l.ID {
					filtered = append(filtered, r)
				}
			}
			runs = filtered
		}
		if len(runs) > 20 {
			runs = runs[:20]
		}
		if hasFlag(rest, "--json") {
			return writeJSON(out, runs)
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "RUN\tLOOP\tSTATUS\tSTARTED\tSUMMARY")
		for _, r := range runs {
			summary := r.Error
			if r.Result != nil {
				summary = r.Result.Summary
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ID[:10], r.Definition.Name, r.Status, r.Started.Format("01-02 15:04"), oneLine(summary))
		}
		return w.Flush()
	case "create":
		file := flagValue(rest, "-f")
		if file == "" {
			return errors.New("create needs -f FILE (or -f - for stdin)")
		}
		var src []byte
		if file == "-" {
			src, err = io.ReadAll(os.Stdin)
		} else {
			src, err = os.ReadFile(file)
		}
		if err != nil {
			return err
		}
		s, err := c.state()
		if err != nil {
			return err
		}
		l, err := draftFromJSON(src, s.Settings)
		if err != nil {
			return err
		}
		before := map[string]bool{}
		for _, x := range s.Loops {
			before[x.ID] = true
		}
		var after core.State
		if err := c.do("POST", "/api/loops", l, &after); err != nil {
			return fmt.Errorf("Loopbee rejected the draft: %w", err)
		}
		for _, x := range after.Loops {
			if !before[x.ID] {
				fmt.Fprintf(out, "Created draft %q (%s), schedule off.\nOpen Loopbee → Loops → %s to review it, run it once, then enable the schedule.\n", x.Name, x.ID, x.Name)
				return nil
			}
		}
		return nil
	case "run":
		if len(rest) == 0 {
			return errors.New("run needs a loop id or name")
		}
		s, err := c.state()
		if err != nil {
			return err
		}
		l, err := findLoop(s, strings.Join(rest, " "))
		if err != nil {
			return err
		}
		var after core.State
		if err := c.do("POST", "/api/loops/"+l.ID+"/run", map[string]any{}, &after); err != nil {
			return err
		}
		for _, r := range sortedRuns(after.Runs) {
			if r.LoopID == l.ID {
				fmt.Fprintf(out, "Started run %s of %q (%s)\n", r.ID[:10], l.Name, r.Status)
				break
			}
		}
		return nil
	}
	usage(out)
	return fmt.Errorf("unknown command %q", cmd)
}

// draftFromJSON fills defaults and forces the safety-relevant fields of a new draft.
func draftFromJSON(src []byte, c core.Settings) (core.Loop, error) {
	l := core.Loop{Sandbox: "read-only", Timezone: c.DigestTimezone, CatchUp: true, Private: true, RequireClean: true,
		DoneWhen: "verify.passed", MaxRounds: 1, WallMinutes: 60, StallRounds: 2, GateHours: 72, Workspace: "direct",
		AllowedTools: []string{}, Agent: c.DefaultAgent}
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return l, fmt.Errorf("invalid loop JSON: %w (see beectl schema)", err)
	}
	if l.Agent == "" {
		l.Agent = "codex"
	}
	if l.Timezone == "" {
		l.Timezone = "UTC"
	}
	l.ID, l.Version, l.Enabled = "", 0, false
	l.Next, l.Updated = time.Time{}, time.Time{}
	if l.Source == "" {
		l.Source = "beectl"
	}
	if l.Workspace == "worktree" {
		l.RequireClean = false
	}
	for _, s := range l.Steps {
		if (s.Kind == "run" || s.Kind == "verify") && len(s.Argv) > 0 {
			found := false
			for _, t := range l.AllowedTools {
				found = found || t == s.Argv[0]
			}
			if !found {
				l.AllowedTools = append(l.AllowedTools, s.Argv[0])
			}
		}
	}
	for i := range l.Env {
		if l.Env[i].Secret && l.Env[i].Value != "" && !strings.HasPrefix(l.Env[i].Value, "env:") {
			return l, fmt.Errorf("secret %s must not be in the definition; leave its value empty and fill it in Loopbee", l.Env[i].Name)
		}
	}
	return l, nil
}

func printSchema(out io.Writer) error {
	schema := core.AuthorSchema()["properties"].(map[string]any)["loop"]
	fmt.Fprintln(out, "Loop definition schema (also accepts \"directory\", \"agent\" and \"env[].value\" for non-secret values):")
	if err := writeJSON(out, schema); err != nil {
		return err
	}
	_, example, _ := strings.Cut(string(skill), "```json\n")
	example, _, _ = strings.Cut(example, "```")
	fmt.Fprintln(out, "\nExample:")
	fmt.Fprint(out, example)
	return nil
}

func skillCommand(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "print" {
		_, err := out.Write(skill)
		return err
	}
	if args[0] != "install" {
		return errors.New("skill print | skill install [--agent codex|claude|all] [--dir DIR]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targets := []string{}
	if dir := flagValue(args, "--dir"); dir != "" {
		targets = append(targets, dir)
	} else {
		agent := flagValue(args, "--agent")
		if agent == "" {
			agent = "all"
		}
		if agent == "claude" || agent == "all" {
			targets = append(targets, filepath.Join(home, ".claude", "skills", "loopbee"))
		}
		if agent == "codex" || agent == "all" {
			targets = append(targets, filepath.Join(home, ".codex", "skills", "loopbee"), filepath.Join(home, ".agents", "skills", "loopbee"))
		}
		if len(targets) == 0 {
			return fmt.Errorf("unknown agent %q", agent)
		}
	}
	for _, t := range targets {
		if err := os.MkdirAll(t, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(t, "SKILL.md"), skill, 0644); err != nil {
			return err
		}
		fmt.Fprintln(out, "installed", filepath.Join(t, "SKILL.md"))
	}
	return nil
}

func sortedRuns(runs []core.Run) []core.Run {
	out := append([]core.Run{}, runs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}
func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}
func flagValue(args []string, f string) string {
	for i, a := range args {
		if a == f && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, f+"="); ok {
			return v
		}
	}
	return ""
}
func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
