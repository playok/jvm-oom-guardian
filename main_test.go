package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHelpContainsOperationalGuide(t *testing.T) {
	var output bytes.Buffer
	usage(&output)
	for _, want := range []string{
		"빠른 시작", "HeapDumpOnOutOfMemoryError", "CrashOnOutOfMemoryError",
		"CATALINA_PID", "server --help", "notify --help",
	} {
		if !bytes.Contains(output.Bytes(), []byte(want)) {
			t.Errorf("help does not contain %q", want)
		}
	}
}

func TestSubcommandHelpContainsDefaults(t *testing.T) {
	var serverOutput, notifyOutput bytes.Buffer
	serverUsage(&serverOutput, "/tmp/config.json")
	notifyUsage(&notifyOutput, "/tmp/daemon.sock")
	if !bytes.Contains(serverOutput.Bytes(), []byte("/tmp/config.json")) {
		t.Fatal("server help does not contain config default")
	}
	if !bytes.Contains(notifyOutput.Bytes(), []byte("/tmp/daemon.sock")) {
		t.Fatal("notify help does not contain socket default")
	}
}

func TestLoadConfigDefaultsAndExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"services":{"tomcat":{"pid_file":"~/tomcat.pid","process_match":"catalina.base=/srv/app","start_command":["/srv/app/bin/startup.sh"]}}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["tomcat"]
	if cfg.Socket != filepath.Join(dir, defaultSocketName) {
		t.Fatalf("socket = %q", cfg.Socket)
	}
	if svc.PIDFile != filepath.Join(dir, "tomcat.pid") {
		t.Fatalf("pid file = %q", svc.PIDFile)
	}
	if svc.ExitWait.Duration != 30*time.Second || svc.StopTimeout.Duration != 10*time.Second {
		t.Fatalf("unexpected defaults: %+v", svc)
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"unknown":true,"services":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path, t.TempDir()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadConfigRejectsInsecurePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"services":{"tomcat":{"pid_file":"/tmp/tomcat.pid","process_match":"catalina","start_command":["startup.sh"]}}}`
	if err := os.WriteFile(path, []byte(data), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path, t.TempDir()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPrepareSocketRefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket")
	if err := os.WriteFile(path, []byte("do not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocket(path); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}

func TestRestartBackoffIsExponentialAndCapped(t *testing.T) {
	svc := serviceConfig{BackoffInitial: duration{time.Second}, BackoffMax: duration{5 * time.Second}}
	for attempt, want := range map[int]time.Duration{0: 0, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 5 * time.Second, 10: 5 * time.Second} {
		if got := restartBackoff(svc, attempt); got != want {
			t.Errorf("attempt %d: got %s want %s", attempt, got, want)
		}
	}
}

func TestOpenRestartLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.log")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := newRollingLogWriter(filepath.Dir(path), filepath.Base(path), "text", true, 3, 2, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil || string(rotated) != "old" {
		t.Fatalf("rotated log = %q, err=%v", rotated, err)
	}
}

func TestRotatingLogWriterWritesJSONAndRotates(t *testing.T) {
	dir := t.TempDir()
	w := &rotatingLogWriter{directory: dir, pattern: "events.log", format: "json", rotate: true, maxBytes: 80, maxFiles: 2, retentionDays: 30}
	if _, err := w.Write([]byte("first event\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("second event that forces rolling\n")); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatalf("expected a rotated log, got %d file(s)", len(files))
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"timestamp"`)) || !bytes.Contains(data, []byte(`"message"`)) {
		t.Fatalf("not JSON log: %s", data)
	}
}
