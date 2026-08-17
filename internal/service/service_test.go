package service

import (
	"strings"
	"testing"
)

func TestRenderLaunchdEscapesPaths(t *testing.T) {
	rendered, err := Render("darwin", "/Applications/Chain & Proof/bin/chainproof", "/tmp/a & b.log")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dev.chainproof.daemon", "/Applications/Chain &amp; Proof/bin/chainproof", "<string>daemon</string>", "<key>KeepAlive</key><true/>", "/tmp/a &amp; b.log"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("launchd config missing %q", want)
		}
	}
}
func TestRenderSystemdQuotesPaths(t *testing.T) {
	rendered, err := Render("linux", "/home/me/Chain Proof/chainproof", "/home/me/chain proof.log")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`ExecStart="/home/me/Chain Proof/chainproof" daemon`, `StandardOutput=append:"/home/me/chain proof.log"`, `WantedBy=default.target`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("systemd unit missing %q\n%s", want, rendered)
		}
	}
}
func TestRenderRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := Render("plan9", "x", "y"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}
