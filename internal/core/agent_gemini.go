package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Gemini CLI: headless with `--output-format stream-json`. Its policy engine turns every "ask" into "deny" when no
// person is present, and `--sandbox` confines the whole CLI (macOS sandbox-exec; Docker or Podman elsewhere, else
// it refuses to start). Levels:
//
//	read-only          --approval-mode default: read and search tools only (Google web search included)
//	workspace-write    --approval-mode auto_edit --sandbox: edits allowed, shell commands denied, writes confined
//	workspace-network  --approval-mode yolo --sandbox: every tool, writes confined to the project, network open
//
// The CLI itself needs the network for its model, so its sandbox cannot cut network off; workspace-write therefore
// denies the shell instead. Skills need activate_skill, which Loopbee allows through a policy file.
func init() {
	registerAdapter(&adapter{
		Name: "gemini", Label: "Gemini CLI", Binary: "gemini",
		NpmEntries: []string{"@google/gemini-cli/bundle/gemini.js", "@google/gemini-cli/dist/index.js"},
		HelpArgs:   []string{"--help"},
		Caps: func(help []byte) []string {
			has := helpHas(help)
			return capList(map[string]bool{
				"headless": has("--output-format", "stream-json", "--approval-mode"), "sandbox": has("--sandbox"),
				"policy": has("--policy"), "include-dirs": has("--include-directories"), "skip-trust": has("--skip-trust"),
				"model": has("--model"),
			})
		},
		Levels:      []string{"read-only", "workspace-write", "workspace-network"},
		Enforcement: map[string]string{"read-only": "tools", "workspace-write": "sandbox", "workspace-network": "sandbox", "agent-config": "agent"},
		Flags: map[string]string{
			"read-only":         "--approval-mode default (read tools only)",
			"workspace-write":   "--approval-mode auto_edit --sandbox (edits, no shell)",
			"workspace-network": "--approval-mode yolo --sandbox",
			"agent-config":      "(no flags — your settings.json)",
		},
		Args:     geminiArgs,
		Classify: geminiClassify,
		Final:    geminiFinal,
		Failure:  geminiFailure,
		Usage:    geminiUsage,
		Context: func(home, _ string) []string {
			gh := filepath.Join(home, ".gemini")
			return []string{filepath.Join(gh, "skills"), filepath.Join(gh, "extensions"), filepath.Join(gh, "commands"),
				filepath.Join(gh, "GEMINI.md"), filepath.Join(home, ".agents", "skills")}
		},
		ToolNote: `Gemini CLI at read-only reads and searches files (and may use Google web search). At workspace-write
  it edits files inside its sandbox but cannot run shell commands: put commands in run steps before the agent step,
  or choose workspace-network, which allows commands and network while writes stay inside the project. Its sandbox
  needs macOS (sandbox-exec) or Docker/Podman.`,
		Explain: func(reason, goos string) string {
			if strings.Contains(reason, "sandbox") && (strings.Contains(reason, "docker") || strings.Contains(reason, "podman") || strings.Contains(reason, "Missing sandbox command")) {
				return reason + " — Gemini CLI's sandbox needs sandbox-exec (macOS) or Docker/Podman; install one, or run this Loop at read-only or with another agent."
			}
			return reason
		},
		MCPServers: func(home, project string) []string {
			gh := filepath.Join(home, ".gemini")
			names := jsonObjectKeys(filepath.Join(gh, "settings.json"), "mcpServers")
			exts, _ := os.ReadDir(filepath.Join(gh, "extensions"))
			for _, e := range exts {
				names = append(names, jsonObjectKeys(filepath.Join(gh, "extensions", e.Name(), "gemini-extension.json"), "mcpServers")...)
			}
			if project != "" {
				names = append(names, jsonObjectKeys(filepath.Join(project, ".gemini", "settings.json"), "mcpServers")...)
			}
			return names
		},
		MCPFlags: map[string]string{
			"on":  `--policy: mcpName = "*" allow (at agent-config next to your own ~/.gemini/policies)`,
			"off": `--policy: mcpName = "*" deny`,
		},
		Refused: func(output []byte) []string {
			return refusedBetween(output, `Tool execution for "`, `" requires user confirmation`, "%s", "")
		},
	})
}

func geminiArgs(inv agentInvocation, caps []string) ([]string, []string) {
	argv := []string{"--output-format", "stream-json"}
	var env []string
	if hasCap(caps, "skip-trust") && inv.Sandbox != "agent-config" {
		// An untrusted folder (every fresh worktree) silently falls back to "default" approval.
		argv = append(argv, "--skip-trust")
	}
	reads := inv.ReadPaths
	switch inv.Sandbox {
	case "read-only":
		argv = append(argv, "--approval-mode", "default")
	case "workspace-write":
		argv = append(argv, "--approval-mode", "auto_edit", "--sandbox")
	case "workspace-network":
		argv = append(argv, "--approval-mode", "yolo", "--sandbox")
		// The shell can read the paths (sandbox-exec reads the whole disk; containers get read-only mounts).
		if len(reads) > 0 {
			mounts := []string{}
			for _, p := range reads {
				mounts = append(mounts, p+":"+p+":ro")
			}
			env = append(env, "SANDBOX_MOUNTS="+strings.Join(mounts, ","))
		}
		reads = nil // --include-directories would make them writable
	}
	if inv.Sandbox != "agent-config" {
		if hasCap(caps, "include-dirs") {
			for _, p := range reads {
				if info, err := os.Stat(p); err == nil && info.IsDir() {
					argv = append(argv, "--include-directories", p)
				}
			}
		}
		if hasCap(caps, "policy") && inv.Scratch != "" {
			if file, err := geminiPolicy(inv.Scratch, inv.Sandbox, reads, inv.MCP); err == nil {
				argv = append(argv, "--policy", file)
			}
		}
	} else if inv.MCP && hasCap(caps, "policy") && inv.Scratch != "" {
		// --policy replaces the user's policy folder, so it is passed too: agent-config keeps the user's own rules.
		if inv.Home != "" {
			if user := filepath.Join(inv.Home, ".gemini", "policies"); existing([]string{user}) != nil {
				argv = append(argv, "--policy", user)
			}
		}
		if file, err := geminiPolicy(inv.Scratch, inv.Sandbox, nil, true); err == nil {
			argv = append(argv, "--policy", file)
		}
	}
	if inv.Model != "" {
		argv = append(argv, "--model", inv.Model)
	}
	return argv, env
}

// geminiPolicy writes the policy rules Loopbee adds: skills may load at every level, and edit tools may not touch
// read paths (they are workspace directories for the read tools only).
func geminiPolicy(dir, level string, readPaths []string, mcp bool) (string, error) {
	var b strings.Builder
	b.WriteString("# Written by Loopbee for one run.\n")
	if level != "agent-config" {
		b.WriteString("[[rule]]\ntoolName = \"activate_skill\"\ndecision = \"allow\"\npriority = 900\n")
	}
	// Every tool of every MCP server: approved in advance (the Loop allows them) or disabled.
	decision := "deny"
	if mcp {
		decision = "allow"
	}
	if mcp || level != "agent-config" {
		fmt.Fprintf(&b, "\n[[rule]]\ntoolName = \"*\"\nmcpName = \"*\"\ndecision = %q\npriority = 990\n", decision)
	}
	if level == "workspace-write" {
		for _, p := range readPaths {
			// Tool arguments are matched as JSON text, where a Windows backslash appears doubled.
			escaped := strings.ReplaceAll(p, `\`, `\\`)
			fmt.Fprintf(&b, "\n[[rule]]\ntoolName = [\"write_file\", \"replace\"]\nargsPattern = %q\ndecision = \"deny\"\npriority = 950\n", regexp.QuoteMeta(escaped))
		}
	}
	file := filepath.Join(dir, "gemini-policy.toml")
	return file, os.WriteFile(file, []byte(b.String()), 0600)
}

func geminiClassify(line string) (string, bool) {
	var ev map[string]any
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "agent.raw", false
	}
	jsonLines([]byte(line), func(_ string, e map[string]any) { ev = e })
	switch t := str(ev, "type"); t {
	case "init", "message", "tool_use", "tool_result", "error":
		return "agent." + t, false
	case "result":
		return "agent.result", str(ev, "status") == "error"
	}
	return "agent.raw", false
}

// geminiFinal is the assistant text after the last tool call (message events stream it in delta chunks).
func geminiFinal(output []byte) (string, map[string]any) {
	var cur strings.Builder
	last := ""
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "message":
			if str(ev, "role") != "assistant" {
				return
			}
			if ev["delta"] != true {
				cur.Reset()
			}
			cur.WriteString(str(ev, "content"))
			last = cur.String()
		case "tool_use", "tool_result":
			cur.Reset()
		}
	})
	return last, nil
}

func geminiFailure(output []byte) string {
	reason := ""
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "result":
			if m := str(obj(ev, "error"), "message"); m != "" {
				reason = m
			}
		case "error":
			if str(ev, "severity") == "error" && str(ev, "message") != "" {
				reason = str(ev, "message")
			}
		}
	})
	if reason == "" {
		// Fatal start-up errors (sandbox, auth) are printed as text, not JSON.
		for _, line := range strings.Split(string(output), "\n") {
			if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "{") && (strings.Contains(l, "Error") || strings.Contains(l, "error")) {
				reason = l
			}
		}
	}
	return reason
}

// geminiUsage reads result.stats: input_tokens includes cached; models lists the models used.
func geminiUsage(output []byte) Usage {
	var u Usage
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "init":
			if u.Model == "" {
				u.Model = str(ev, "model")
			}
		case "result":
			st := obj(ev, "stats")
			cached := num(st, "cached")
			u.Input += max(num(st, "input_tokens")-cached, 0)
			u.CacheRead += cached
			u.Output += num(st, "output_tokens")
			if models := obj(st, "models"); len(models) > 0 {
				names := []string{}
				for name := range models {
					names = append(names, name)
				}
				slices.Sort(names)
				u.Model = strings.Join(names, ", ")
			}
		}
	})
	return u
}
