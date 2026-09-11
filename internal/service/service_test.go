package service

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLaunchdEscapesPaths(t *testing.T) {
	config := Config{
		Database:      "/Users/me/Chain & Proof/chainproof.db",
		AgentHome:     "/Users/me/Agents & Helpers",
		AgentProfile:  "builder<&>",
		CodexRoot:     "/Users/me/.codex & sessions",
		CodexContent:  "full",
		CodexDisabled: "1",
	}
	rendered, err := Render("darwin", "/Applications/Chain & Proof/bin/chainproof", "/tmp/a & b.log", config)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"dev.chainproof.daemon",
		"/Applications/Chain &amp; Proof/bin/chainproof",
		"<string>daemon</string>",
		"<key>KeepAlive</key><true/>",
		"/tmp/a &amp; b.log",
		"<key>CHAINPROOF_DB</key><string>/Users/me/Chain &amp; Proof/chainproof.db</string>",
		"<key>CHAINPROOF_AGENT_HOME</key><string>/Users/me/Agents &amp; Helpers</string>",
		"<key>CHAINPROOF_AGENT_PROFILE</key><string>builder&lt;&amp;&gt;</string>",
		"<key>CHAINPROOF_CODEX_ROOT</key><string>/Users/me/.codex &amp; sessions</string>",
		"<key>CHAINPROOF_CODEX_CONTENT</key><string>full</string>",
		"<key>CHAINPROOF_CODEX_DISABLED</key><string>1</string>",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("launchd config missing %q", want)
		}
	}
	if strings.Contains(rendered, "SECRET") {
		t.Fatal("launchd config captured unconfigured ambient secret")
	}
}
func TestRenderSystemdQuotesPaths(t *testing.T) {
	config := Config{
		Database:     `/home/me/Chain Proof/chainproof.db`,
		AgentHome:    `/home/me/agents`,
		AgentProfile: `builder "one"`,
	}
	rendered, err := Render("linux", "/home/me/Chain Proof/chainproof", "/home/me/chain proof.log", config)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ExecStart="/home/me/Chain Proof/chainproof" daemon`,
		`StandardOutput=append:"/home/me/chain proof.log"`,
		`Environment="CHAINPROOF_DB=/home/me/Chain Proof/chainproof.db"`,
		`Environment="CHAINPROOF_AGENT_HOME=/home/me/agents"`,
		`Environment="CHAINPROOF_AGENT_PROFILE=builder \"one\""`,
		`WantedBy=default.target`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("systemd unit missing %q\n%s", want, rendered)
		}
	}
	if count := strings.Count(rendered, "Restart=on-failure"); count != 1 {
		t.Fatalf("systemd restart policy rendered %d times\n%s", count, rendered)
	}
}
func TestRenderRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := Render("plan9", "x", "y", Config{}); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestLogPathUsesConfiguredDatabaseDirectory(t *testing.T) {
	database := filepath.Join("srv", "chainproof", "state", "ledger.db")
	want := filepath.Join("srv", "chainproof", "state", "daemon.log")
	got := LogPath(Config{Database: database}, filepath.Join("home", "me", ".chainproof", "daemon.log"))
	if got != want {
		t.Fatalf("LogPath() = %q", got)
	}
}
