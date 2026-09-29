// Package gateway manages IBKR's Client Portal Gateway, the local Java proxy an individual
// account has to log in through: locate or download it, find or install Java, put it on a
// free port, start and stop it, wait for it, open the browser at its login page and
// keep the login alive. Every step checks first, so Setup is safe to repeat.
//
// It is the one implementation: cmd/ibkr (`ibkr gateway ...`) and the scripts call it.
// It never touches your IBKR password: the login (username, password, 2FA) is yours, in
// the browser.
package gateway

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// ZipURL is where IBKR publishes the gateway.
	ZipURL = "https://download2.interactivebrokers.com/portal/clientportal.gw.zip"
	// JarRel is the gateway's jar relative to its directory; its presence means "installed".
	JarRel = "dist/ibgroup.web.core.iblink.router.clientportal.gw.jar"
	// DefaultPort avoids 5000, which macOS AirPlay Receiver owns.
	DefaultPort = 5001
)

// Manager is one gateway installation.
type Manager struct {
	Dir  string // the gateway directory (contains bin/, root/, dist/)
	Port int
	Out  io.Writer // progress; nil discards
}

func (m *Manager) say(format string, a ...any) {
	if m.Out != nil {
		fmt.Fprintf(m.Out, format+"\n", a...)
	}
}

func (m *Manager) port() int {
	if m.Port > 0 {
		return m.Port
	}
	return DefaultPort
}

// URL is the gateway's address.
func (m *Manager) URL() string { return "https://localhost:" + strconv.Itoa(m.port()) }

func (m *Manager) pidFile() string  { return filepath.Join(m.Dir, "logs", "gateway.pid") }
func (m *Manager) logFile() string  { return filepath.Join(m.Dir, "logs", "gateway.out") }
func (m *Manager) confFile() string { return filepath.Join(m.Dir, "root", "conf.yaml") }

// DefaultDirs are where an existing installation is looked for, in order.
func DefaultDirs(explicit string) []string {
	home, _ := os.UserHomeDir()
	return []string{explicit, os.Getenv("IBKR_CPGW_DIR"),
		filepath.Join(home, "Documents", "Archive", "ibkr_personal", "clientportal"),
		filepath.Join(home, "ibkr", "clientportal")}
}

// Locate returns the first directory in dirs that holds the gateway jar.
func Locate(dirs ...string) (string, bool) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, JarRel)); err == nil {
			return d, true
		}
	}
	return "", false
}

// Download fetches ZipURL and unpacks it into dest.
func Download(ctx context.Context, dest string, out io.Writer) error {
	if out != nil {
		fmt.Fprintf(out, "downloading %s -> %s\n", ZipURL, dest)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ZipURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return unzip(data, dest)
}

func unzip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range zr.File {
		target := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(target, root) && filepath.Clean(target) != filepath.Clean(dest) {
			return fmt.Errorf("zip entry %q escapes the destination", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		w.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// FindJava returns a JAVA_HOME with a working java (11+ is what the gateway needs). macOS's
// /usr/bin/java is only a stub that fails, so each candidate is actually run.
func FindJava() (string, bool) {
	cands := []string{os.Getenv("JAVA_HOME")}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("/usr/libexec/java_home").Output(); err == nil {
			cands = append(cands, strings.TrimSpace(string(out)))
		}
	}
	cands = append(cands,
		"/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home",
		"/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home",
		"/usr/local/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home",
		"/usr/lib/jvm/default-java", "/usr/lib/jvm/java-17-openjdk-amd64")
	for _, jh := range cands {
		if jh == "" {
			continue
		}
		if err := exec.Command(filepath.Join(jh, "bin", "java"), "-version").Run(); err == nil {
			return jh, true
		}
	}
	return "", false
}

// InstallJava installs OpenJDK 17 with Homebrew.
func InstallJava(ctx context.Context, out io.Writer) error {
	brew, err := exec.LookPath("brew")
	if err != nil {
		return errors.New("Java is missing and Homebrew is not installed: install a JDK 11+ yourself (https://brew.sh, then `brew install openjdk@17`)")
	}
	cmd := exec.CommandContext(ctx, brew, "install", "openjdk@17")
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

var listenPortRE = regexp.MustCompile(`(?m)^listenPort:.*$`)

// ConfigurePort makes root/conf.yaml listen on the Manager's port. The original is kept
// once as conf.yaml.orig. It reports whether it changed anything.
func (m *Manager) ConfigurePort() (bool, error) {
	data, err := os.ReadFile(m.confFile())
	if err != nil {
		return false, err
	}
	want := "listenPort: " + strconv.Itoa(m.port())
	if bytes.Contains(data, []byte("\n"+want)) || bytes.HasPrefix(data, []byte(want)) {
		return false, nil
	}
	if _, err := os.Stat(m.confFile() + ".orig"); err != nil {
		if err := os.WriteFile(m.confFile()+".orig", data, 0o644); err != nil {
			return false, err
		}
	}
	return true, os.WriteFile(m.confFile(), listenPortRE.ReplaceAll(data, []byte(want)), 0o644)
}

// portOwner names what is listening on the port, "" when nothing is.
func portOwner(port int) string {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return ""
	}
	c.Close()
	if out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output(); err == nil {
		if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) > 1 {
			return strings.Fields(lines[1])[0]
		}
	}
	return "another process"
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402: loopback gateway, self-signed by IBKR
}

// Status is the gateway's own answer about the login session.
type Status struct {
	Authenticated bool `json:"authenticated"`
	Connected     bool `json:"connected"`
	Competing     bool `json:"competing"`
}

// Alive reports whether anything answers HTTP on the gateway's port.
func (m *Manager) Alive(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.URL()+"/v1/api/iserver/auth/status", strings.NewReader(""))
	req.Header.Set("User-Agent", "ibkr_api-go/1.0")
	resp, err := httpClient().Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// Session asks whether the browser login is in place.
func (m *Manager) Session(ctx context.Context) (Status, error) {
	var st Status
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.URL()+"/v1/api/iserver/auth/status", strings.NewReader(""))
	req.Header.Set("User-Agent", "ibkr_api-go/1.0")
	resp, err := httpClient().Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return st, nil // reachable, not logged in
	}
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("auth/status: HTTP %d", resp.StatusCode)
	}
	return st, json.Unmarshal(body, &st)
}

// Start launches the gateway detached (it survives this process), logging to
// logs/gateway.out, and returns its pid. javaHome is a working JAVA_HOME.
func (m *Manager) Start(javaHome string) (int, error) {
	if err := os.MkdirAll(filepath.Join(m.Dir, "logs"), 0o755); err != nil {
		return 0, err
	}
	logf, err := os.OpenFile(m.logFile(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()
	cmd := exec.Command(filepath.Join(m.Dir, "bin", "run.sh"), filepath.Join("root", "conf.yaml"))
	cmd.Dir = m.Dir
	cmd.Env = append(os.Environ(), "JAVA_HOME="+javaHome, "PATH="+filepath.Join(javaHome, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(m.pidFile(), []byte(strconv.Itoa(pid)), 0o644)
	_ = cmd.Process.Release()
	return pid, nil
}

// Stop ends the gateway this Manager started (the pid in logs/gateway.pid), including its
// java child.
func (m *Manager) Stop() (bool, error) {
	data, err := os.ReadFile(m.pidFile())
	if err != nil {
		return false, nil
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		os.Remove(m.pidFile())
		return false, nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM) // the whole process group (run.sh + java)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	os.Remove(m.pidFile())
	return true, nil
}

// WaitReady blocks until the gateway answers or timeout passes.
func (m *Manager) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.Alive(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	tail, _ := os.ReadFile(m.logFile())
	if len(tail) > 1500 {
		tail = tail[len(tail)-1500:]
	}
	return fmt.Errorf("the gateway did not answer at %s within %s; log %s:\n%s", m.URL(), timeout, m.logFile(), tail)
}

// OpenBrowser opens url in Chrome (macOS), else the system default.
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/Applications/Google Chrome.app"); err == nil {
			return exec.Command("open", "-a", "Google Chrome", url).Run()
		}
		return exec.Command("open", url).Run()
	case "linux":
		return exec.Command("xdg-open", url).Run()
	}
	return fmt.Errorf("don't know how to open a browser on %s: open %s yourself", runtime.GOOS, url)
}

// Keepalive tickles the session every interval until ctx ends, so it does not idle out
// (IBKR drops it after about six minutes). It returns the first error.
func (m *Manager) Keepalive(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.URL()+"/v1/api/tickle", strings.NewReader(""))
		req.Header.Set("User-Agent", "ibkr_api-go/1.0")
		resp, err := httpClient().Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// SetupOptions drives Setup.
type SetupOptions struct {
	Dir        string // explicit gateway directory ("" = search DefaultDirs, else download)
	Port       int
	Start      bool
	InstallDir string // where a download goes (default ~/ibkr/clientportal)
}

// Setup does whatever is not yet done to get a running gateway: files, Java, port,
// process. It reports each step to m.Out and returns the ready Manager.
func Setup(ctx context.Context, opts SetupOptions, out io.Writer) (*Manager, error) {
	m := &Manager{Port: opts.Port, Out: out}
	m.say("1. gateway files")
	if dir, ok := Locate(DefaultDirs(opts.Dir)...); ok {
		m.Dir = dir
		m.say("   done: %s", dir)
	} else {
		dest := opts.InstallDir
		if dest == "" {
			home, _ := os.UserHomeDir()
			dest = filepath.Join(home, "ibkr", "clientportal")
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return nil, err
		}
		if err := Download(ctx, dest, out); err != nil {
			return nil, err
		}
		if _, ok := Locate(dest); !ok {
			return nil, fmt.Errorf("the download did not contain %s", JarRel)
		}
		_ = os.Chmod(filepath.Join(dest, "bin", "run.sh"), 0o755)
		m.Dir = dest
		m.say("   downloaded into %s", dest)
	}
	m.say("2. Java")
	javaHome, ok := FindJava()
	if ok {
		m.say("   done: %s", javaHome)
	} else {
		m.say("   not found: installing openjdk@17 with Homebrew")
		if err := InstallJava(ctx, out); err != nil {
			return nil, err
		}
		if javaHome, ok = FindJava(); !ok {
			return nil, errors.New("Java was installed but no working JDK was found")
		}
	}
	m.say("3. port %d", m.port())
	changed, err := m.ConfigurePort()
	if err != nil {
		return nil, err
	}
	if changed {
		m.say("   conf.yaml now listens on %d (original saved as conf.yaml.orig)", m.port())
	} else {
		m.say("   done: conf.yaml already listens on %d", m.port())
	}
	if !opts.Start {
		return m, nil
	}
	m.say("4. process")
	if m.Alive(ctx) {
		m.say("   done: already answering at %s", m.URL())
		return m, nil
	}
	if owner := portOwner(m.port()); owner != "" {
		return nil, fmt.Errorf("port %d is used by %s and is not the gateway; pick another port", m.port(), owner)
	}
	pid, err := m.Start(javaHome)
	if err != nil {
		return nil, err
	}
	m.say("   started (pid %d), waiting for %s ...", pid, m.URL())
	if err := m.WaitReady(ctx, 60*time.Second); err != nil {
		return nil, err
	}
	m.say("   up.")
	return m, nil
}

// Login makes sure the gateway is up, opens the login page when there is no session, and
// waits (up to wait) for you to finish logging in. It returns once authenticated.
func (m *Manager) Login(ctx context.Context, wait time.Duration, open bool) error {
	if !m.Alive(ctx) {
		return fmt.Errorf("the gateway is not answering at %s (run `ibkr gateway start`)", m.URL())
	}
	if st, err := m.Session(ctx); err == nil && st.Authenticated {
		m.say("already logged in.")
		return nil
	}
	m.say("log in at %s (username, password, 2FA); waiting up to %s ...", m.URL(), wait)
	if open {
		if err := OpenBrowser(m.URL()); err != nil {
			m.say("could not open the browser: %v", err)
		}
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if st, err := m.Session(ctx); err == nil && st.Authenticated {
			m.say("logged in.")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("not logged in after %s", wait)
}
