package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The settings file belongs to the user and holds hooks from many tools. The
// one unacceptable outcome is losing any of it, so this fixture carries a
// foreign hook on an event basanite also writes to, plus unrelated top-level
// keys.
const fixture = `{
  "model": "opus",
  "permissions": {"allow": ["Bash(git *)"]},
  "hooks": {
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "/usr/bin/other inject"}]},
      {"matcher": "", "hooks": [{"type": "command", "command": "/old/path/basanite refresh", "async": true}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/bin/guard"}]}
    ]
  }
}`

func loadFixture(t *testing.T) (*Settings, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestApplyPreservesEverythingElse(t *testing.T) {
	s, _ := loadFixture(t)
	s.Apply("/new/bin/basanite")

	var got map[string]any
	b, _ := s.Bytes()
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "opus" {
		t.Errorf("an unrelated top-level key was lost: %v", got["model"])
	}
	if _, ok := got["permissions"]; !ok {
		t.Error("permissions were lost")
	}
	hooks := got["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("an event basanite does not use was dropped")
	}
	if !strings.Contains(string(b), "/usr/bin/other inject") {
		t.Error("a foreign hook sharing an event with ours was lost")
	}
}

// Re-running after installing to a new path must repoint the hook, not stack a
// second registration that keeps running the old binary too.
func TestApplyRetargetsInsteadOfDuplicating(t *testing.T) {
	s, _ := loadFixture(t)
	changes := s.Apply("/new/bin/basanite")

	byEvent := map[string]Change{}
	for _, c := range changes {
		byEvent[c.Hook.Event] = c
	}
	if c := byEvent["SessionStart"]; c.Action != "updated" || c.Was != "/old/path/basanite refresh" {
		t.Errorf("stale registration should be updated in place, got %+v", c)
	}
	if c := byEvent["MessageDisplay"]; c.Action != "added" {
		t.Errorf("a missing hook should be added, got %+v", c)
	}

	b, _ := s.Bytes()
	if strings.Contains(string(b), "/old/path/basanite") {
		t.Errorf("the old path survived:\n%s", b)
	}
	if n := strings.Count(string(b), "basanite refresh"); n != 1 {
		t.Errorf("want exactly 1 refresh registration, got %d:\n%s", n, b)
	}

	// Idempotent: a second identical run changes nothing.
	for _, c := range s.Apply("/new/bin/basanite") {
		if c.Action != "unchanged" {
			t.Errorf("re-running a settled install reported %q for %s", c.Action, c.Hook.Event)
		}
	}
}

func TestRemoveLeavesForeignHooksAlone(t *testing.T) {
	s, _ := loadFixture(t)
	s.Apply("/new/bin/basanite")
	s.Remove()

	b, _ := s.Bytes()
	if strings.Contains(string(b), "basanite") {
		t.Errorf("a basanite registration survived uninstall:\n%s", b)
	}
	if !strings.Contains(string(b), "/usr/bin/other inject") {
		t.Errorf("uninstall took a foreign hook with it:\n%s", b)
	}
	if !strings.Contains(string(b), "/usr/bin/guard") {
		t.Errorf("uninstall dropped an unrelated event:\n%s", b)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	if _, ok := got["hooks"].(map[string]any)["MessageDisplay"]; ok {
		t.Error("an event left with no hooks should be dropped, not left empty")
	}
}

func TestSaveBacksUpAndRoundTrips(t *testing.T) {
	s, path := loadFixture(t)
	s.Apply("/new/bin/basanite")
	backup, err := s.Save(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(backup); err != nil || string(b) != fixture {
		t.Errorf("the backup must hold the original bytes verbatim (err=%v)", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reg := reloaded.Registered()
	if reg["MessageDisplay"] != "/new/bin/basanite display" {
		t.Errorf("registration did not survive the write: %v", reg)
	}
	if len(reg) != len(Hooks) {
		t.Errorf("want all %d hooks registered, got %v", len(Hooks), reg)
	}
}

func TestLoadMissingFileIsEmptyNotError(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing settings file is a fine starting point, got %v", err)
	}
	if len(s.Registered()) != 0 {
		t.Error("nothing should be registered in an empty file")
	}
	if changes := s.Apply("/bin/basanite"); len(changes) != len(Hooks) {
		t.Errorf("want %d hooks added, got %d", len(Hooks), len(changes))
	}
}

func TestLoadRejectsMalformedRatherThanClobbering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("malformed settings must be an error — overwriting them loses the file")
	}
}

func TestIsBasaniteSub(t *testing.T) {
	cases := []struct {
		cmd, sub string
		want     bool
	}{
		{"/home/x/go/bin/basanite hook", "hook", true},
		{"basanite display", "display", true},
		{"/home/x/go/bin/basanite hook", "display", false},
		{"/usr/bin/basanite-other hook", "hook", false},
		{"/usr/bin/other basanite hook", "hook", false},
		{"basanite", "hook", false},
		{"", "hook", false},
	}
	for _, c := range cases {
		if got := isBasaniteSub(c.cmd, c.sub); got != c.want {
			t.Errorf("isBasaniteSub(%q, %q) = %v, want %v", c.cmd, c.sub, got, c.want)
		}
	}
}
