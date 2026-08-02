// Package install registers basanite's hooks in a Claude Code settings file.
//
// The hooks are the whole product — a report nothing reads changes nothing —
// and registering three of them by hand means editing nested JSON and pasting
// an absolute path the docs can only write as "/home/you/...". This does it
// from the running binary, which knows where it actually is.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Hook is one registration: a subcommand, the event it runs on, and why.
type Hook struct {
	Event string
	Sub   string
	Async bool
	Why   string
}

// Hooks is the full turn-start loop. Order is the order they run in a session.
var Hooks = []Hook{
	{Event: "SessionStart", Sub: "refresh", Async: true,
		Why: "regenerate the report when it goes stale"},
	{Event: "UserPromptSubmit", Sub: "hook",
		Why: "inject tic awareness at turn start"},
	{Event: "MessageDisplay", Sub: "display",
		Why: "show the demote rung instead of the tic"},
}

// Change describes one hook's outcome, for the report to the user.
type Change struct {
	Hook   Hook
	Action string // "added", "updated", "unchanged"
	Was    string // the previous command, when updated
}

// Settings is a Claude Code settings file held as generic JSON so that every
// key basanite does not know about survives the round-trip. Only the hooks
// this tool owns are ever touched.
type Settings struct {
	raw map[string]any
}

// Load reads a settings file; a missing one is an empty settings object, since
// registering a hook is a reasonable way to create it.
func Load(path string) (*Settings, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Settings{raw: map[string]any{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return &Settings{raw: m}, nil
}

// Apply registers every hook at bin, returning what changed. An existing
// basanite registration for the same subcommand is rewritten in place rather
// than duplicated, so re-running after `go install` to a new path repoints the
// hooks instead of stacking a second copy that runs the old binary too.
func (s *Settings) Apply(bin string) []Change {
	hooks, _ := s.raw["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		s.raw["hooks"] = hooks
	}
	var changes []Change
	for _, h := range Hooks {
		want := bin + " " + h.Sub
		groups, _ := hooks[h.Event].([]any)
		if found, was := retarget(groups, h.Sub, want); found {
			action := "updated"
			if was == want {
				action = "unchanged"
			}
			changes = append(changes, Change{Hook: h, Action: action, Was: was})
			continue
		}
		entry := map[string]any{"type": "command", "command": want}
		if h.Async {
			entry["async"] = true
		}
		hooks[h.Event] = append(groups, map[string]any{
			"matcher": "",
			"hooks":   []any{entry},
		})
		changes = append(changes, Change{Hook: h, Action: "added"})
	}
	return changes
}

// retarget points an existing basanite registration for sub at want, in place.
// It matches on the subcommand rather than the full path precisely so that a
// hook left over from an older install location is repaired, not duplicated.
func retarget(groups []any, sub, want string) (found bool, was string) {
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		inner, _ := gm["hooks"].([]any)
		for _, e := range inner {
			em, _ := e.(map[string]any)
			cmd, _ := em["command"].(string)
			if !isBasaniteSub(cmd, sub) {
				continue
			}
			em["command"] = want
			return true, cmd
		}
	}
	return false, ""
}

// isBasaniteSub reports whether cmd invokes `basanite <sub>`, at any path.
func isBasaniteSub(cmd, sub string) bool {
	f := strings.Fields(cmd)
	if len(f) < 2 || f[1] != sub {
		return false
	}
	return filepath.Base(f[0]) == "basanite"
}

// Remove strips every basanite registration, dropping groups left empty. The
// uninstall path exists because a hook that fails silently by design is one
// you cannot turn off by watching it.
func (s *Settings) Remove() []Change {
	hooks, _ := s.raw["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	var changes []Change
	for _, h := range Hooks {
		groups, _ := hooks[h.Event].([]any)
		kept := make([]any, 0, len(groups))
		removed := false
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			inner, _ := gm["hooks"].([]any)
			keptInner := make([]any, 0, len(inner))
			for _, e := range inner {
				em, _ := e.(map[string]any)
				cmd, _ := em["command"].(string)
				if isBasaniteSub(cmd, h.Sub) {
					removed = true
					continue
				}
				keptInner = append(keptInner, e)
			}
			if len(keptInner) == 0 {
				continue // the group held only our hook
			}
			gm["hooks"] = keptInner
			kept = append(kept, gm)
		}
		if len(kept) == 0 {
			delete(hooks, h.Event)
		} else {
			hooks[h.Event] = kept
		}
		action := "unchanged"
		if removed {
			action = "removed"
		}
		changes = append(changes, Change{Hook: h, Action: action})
	}
	if len(hooks) == 0 {
		delete(s.raw, "hooks")
	}
	return changes
}

// Registered reports the command currently registered for each hook, for the
// status view — the answer to "is it actually on?", which a tracker cannot
// give and only the live file can.
func (s *Settings) Registered() map[string]string {
	out := map[string]string{}
	hooks, _ := s.raw["hooks"].(map[string]any)
	for _, h := range Hooks {
		groups, _ := hooks[h.Event].([]any)
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			inner, _ := gm["hooks"].([]any)
			for _, e := range inner {
				em, _ := e.(map[string]any)
				if cmd, _ := em["command"].(string); isBasaniteSub(cmd, h.Sub) {
					out[h.Event] = cmd
				}
			}
		}
	}
	return out
}

// Bytes renders the settings as they will be written.
func (s *Settings) Bytes() ([]byte, error) {
	b, err := json.MarshalIndent(s.raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Save backs up the current file, then writes atomically via an exclusive temp
// file and a rename. The backup is the point: this edits a file the user did
// not write and cannot easily reconstruct.
func (s *Settings) Save(path string) (backup string, err error) {
	b, err := s.Bytes()
	if err != nil {
		return "", err
	}
	if old, err := os.ReadFile(path); err == nil {
		backup = path + ".basanite-backup"
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", err
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return backup, err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return backup, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Close(); err != nil {
		return backup, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return backup, err
	}
	return backup, os.Rename(tmp.Name(), path)
}

// Settled reports whether every hook is already where it should be, so a
// re-run can skip the write. Rewriting on a no-op would replace the backup
// with the already-modified file, quietly losing the only copy of what the
// settings looked like before basanite first touched them.
func Settled(changes []Change) bool {
	for _, c := range changes {
		if c.Action != "unchanged" {
			return false
		}
	}
	return len(changes) > 0
}

// DefaultPath is the user-level Claude Code settings file.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Render formats the changes for the terminal.
func Render(changes []Change, bin string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "basanite %s\n\n", bin)
	for _, c := range changes {
		fmt.Fprintf(&b, "  %-9s %-17s %s\n", c.Action, c.Hook.Event, c.Hook.Why)
		if c.Action == "updated" && c.Was != "" {
			fmt.Fprintf(&b, "  %-9s %-17s was: %s\n", "", "", c.Was)
		}
	}
	return b.String()
}

// RenderStatus formats what is registered right now.
func RenderStatus(reg map[string]string, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "hooks in %s\n\n", path)
	for _, h := range Hooks {
		mark, detail := "not registered", ""
		if cmd, ok := reg[h.Event]; ok {
			mark, detail = "registered", cmd
		}
		fmt.Fprintf(&b, "  %-17s %-15s %s\n", h.Event, mark, detail)
	}
	return b.String()
}
