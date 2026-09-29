package gateway

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnzipRefusesAPathThatEscapes(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../evil.txt")
	w.Write([]byte("x"))
	zw.Close()
	if err := unzip(buf.Bytes(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("want an escape error, got %v", err)
	}
}

func TestUnzipLocateAndConfigurePort(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{JarRel: "jar", "root/conf.yaml": "ip:\n  allow: x\nlistenPort: 5000\nlistenSsl: true\n"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	dir := t.TempDir()
	if err := unzip(buf.Bytes(), dir); err != nil {
		t.Fatal(err)
	}
	if got, ok := Locate("", filepath.Join(dir, "nope"), dir); !ok || got != dir {
		t.Fatalf("Locate = %q %v", got, ok)
	}
	m := &Manager{Dir: dir, Port: 5001}
	changed, err := m.ConfigurePort()
	if err != nil || !changed {
		t.Fatalf("first ConfigurePort: changed=%v err=%v", changed, err)
	}
	conf, _ := os.ReadFile(m.confFile())
	if !strings.Contains(string(conf), "listenPort: 5001") || strings.Contains(string(conf), "5000") || !strings.Contains(string(conf), "listenSsl: true") {
		t.Errorf("conf = %s", conf)
	}
	if orig, _ := os.ReadFile(m.confFile() + ".orig"); !strings.Contains(string(orig), "listenPort: 5000") {
		t.Errorf("original not kept: %s", orig)
	}
	if changed, _ := m.ConfigurePort(); changed {
		t.Error("second ConfigurePort must change nothing")
	}
}
