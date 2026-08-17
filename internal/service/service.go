package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
)

const launchdLabel = "dev.chainproof.daemon"

type Paths struct{ Config, Log string }

func Install(executable string) (Paths, error) {
	paths, err := PathsFor(runtime.GOOS)
	if err != nil {
		return paths, err
	}
	if err = os.MkdirAll(filepath.Dir(paths.Config), 0700); err != nil {
		return paths, err
	}
	if err = os.MkdirAll(filepath.Dir(paths.Log), 0700); err != nil {
		return paths, err
	}
	content, err := Render(runtime.GOOS, executable, paths.Log)
	if err != nil {
		return paths, err
	}
	if err = os.WriteFile(paths.Config, []byte(content), 0644); err != nil {
		return paths, err
	}
	switch runtime.GOOS {
	case "darwin":
		uid := os.Getuid()
		_, _ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, launchdLabel))
		if _, err = run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), paths.Config); err != nil {
			return paths, err
		}
		_, err = run("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, launchdLabel))
	case "linux":
		if _, err = run("systemctl", "--user", "daemon-reload"); err == nil {
			_, err = run("systemctl", "--user", "enable", "--now", "chainproof.service")
		}
	}
	return paths, err
}
func Start() error {
	switch runtime.GOOS {
	case "darwin":
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
		if _, err := run("launchctl", "kickstart", "-k", target); err == nil {
			return nil
		}
		paths, pathErr := PathsFor("darwin")
		if pathErr != nil {
			return pathErr
		}
		if _, err := run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), paths.Config); err != nil {
			return err
		}
		_, err := run("launchctl", "kickstart", "-k", target)
		return err
	case "linux":
		_, err := run("systemctl", "--user", "start", "chainproof.service")
		return err
	}
	return unsupported()
}
func Stop() error {
	switch runtime.GOOS {
	case "darwin":
		_, err := run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
		return err
	case "linux":
		_, err := run("systemctl", "--user", "stop", "chainproof.service")
		return err
	}
	return unsupported()
}
func Status() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
	case "linux":
		return run("systemctl", "--user", "status", "chainproof.service", "--no-pager")
	}
	return "", unsupported()
}
func Uninstall() error {
	paths, err := PathsFor(runtime.GOOS)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		_, _ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
	case "linux":
		_, _ = run("systemctl", "--user", "disable", "--now", "chainproof.service")
	}
	if err = os.Remove(paths.Config); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if runtime.GOOS == "linux" {
		_, _ = run("systemctl", "--user", "daemon-reload")
	}
	return nil
}
func PathsFor(goos string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	switch goos {
	case "darwin":
		return Paths{filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), filepath.Join(home, ".chainproof", "daemon.log")}, nil
	case "linux":
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return Paths{filepath.Join(config, "systemd", "user", "chainproof.service"), filepath.Join(home, ".chainproof", "daemon.log")}, nil
	}
	return Paths{}, unsupported()
}
func Render(goos, executable, logPath string) (string, error) {
	data := map[string]string{}
	var source string
	switch goos {
	case "darwin":
		source = plistTemplate
		data["Executable"] = xmlText(executable)
		data["Log"] = xmlText(logPath)
	case "linux":
		source = unitTemplate
		data["Executable"] = strconv.Quote(executable)
		data["Log"] = strconv.Quote(logPath)
	default:
		return "", unsupported()
	}
	tmpl, err := template.New("service").Parse(source)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err = tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
func unsupported() error {
	current, _ := user.Current()
	name := runtime.GOOS
	if current != nil {
		name += " for " + current.Username
	}
	return fmt.Errorf("service management is supported on macOS and Linux (current: %s)", name)
}
func xmlText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.chainproof.daemon</string>
  <key>ProgramArguments</key><array><string>{{.Executable}}</string><string>daemon</string></array>
  <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>{{.Log}}</string>
  <key>StandardErrorPath</key><string>{{.Log}}</string>
</dict></plist>
`
const unitTemplate = `[Unit]
Description=ChainProof local agent provenance daemon
After=default.target

[Service]
Type=simple
ExecStart={{.Executable}} daemon
Restart=on-failure
RestartSec=2
StandardOutput=append:{{.Log}}
StandardError=append:{{.Log}}

[Install]
WantedBy=default.target
`
