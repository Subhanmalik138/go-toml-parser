package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "tomlq")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")
	content := "[server]\nhost = \"localhost\"\nport = 8080\n\n[server.tls]\nenabled = true\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return path
}

func TestCLIWholeDocument(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, writeSample(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("tomlq failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"port": 8080`) || !strings.Contains(string(out), `"host": "localhost"`) {
		t.Errorf("unexpected JSON output:\n%s", out)
	}
}

func TestCLIGet(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "-get", "server.tls.enabled", writeSample(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("tomlq -get failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "true" {
		t.Errorf("tomlq -get = %q, want true", out)
	}
	out, err = exec.Command(bin, "-get", "server.host", writeSample(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("tomlq -get failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "localhost" {
		t.Errorf("tomlq -get host = %q, want localhost", out)
	}
}

func TestCLIValidateAndErrors(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "-validate", writeSample(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("tomlq -validate failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "valid TOML" {
		t.Errorf("validate output = %q", out)
	}

	// Missing key exits non-zero.
	if err := exec.Command(bin, "-get", "nope", writeSample(t)).Run(); err == nil {
		t.Error("tomlq -get missing key should exit non-zero")
	}
	// Invalid TOML exits non-zero.
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.toml")
	os.WriteFile(bad, []byte("a = [1, 2\n"), 0o644)
	if err := exec.Command(bin, bad).Run(); err == nil {
		t.Error("tomlq on invalid TOML should exit non-zero")
	}
}
