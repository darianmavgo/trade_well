// Package runtimeenv detects where the orchestrator binary is running so the
// same CLI can behave identically locally and on App Engine.
package runtimeenv

import (
	"fmt"
	"os"
	"strings"
)

// Kind is the hosting / invocation context.
type Kind string

const (
	Local Kind = "local"
	GAE   Kind = "gae" // App Engine Standard instance
)

// Info is the detected runtime.
type Info struct {
	Kind Kind

	// Service is the GAE service name.
	Service string
	// Version is the GAE version or instance id.
	Version string
	// Project is GCP project id when known.
	Project string
}

// Detect inspects well-known env vars. Safe to call at process start and again
// per request (GAE cron is still Kind=GAE; use IsGAECronHeader for the request).
func Detect() Info {
	info := Info{
		Project: first(os.Getenv("GOOGLE_CLOUD_PROJECT")),
	}
	if v := os.Getenv("GAE_APPLICATION"); v != "" && info.Project == "" {
		if i := strings.IndexByte(v, '~'); i >= 0 {
			info.Project = v[i+1:]
		} else {
			info.Project = v
		}
	}

	// App Engine Standard
	if os.Getenv("GAE_ENV") != "" || os.Getenv("GAE_INSTANCE") != "" ||
		os.Getenv("GAE_SERVICE") != "" || os.Getenv("GAE_RUNTIME") != "" {
		info.Kind = GAE
		info.Service = first(os.Getenv("GAE_SERVICE"), "default")
		info.Version = first(os.Getenv("GAE_VERSION"), os.Getenv("GAE_INSTANCE"))
		return info
	}

	info.Kind = Local
	return info
}

// ShouldAutoServe reports whether a no-args process start should open the HTTP
// server (dashboard + cron + health). True on GAE.
func (i Info) ShouldAutoServe() bool {
	return i.Kind == GAE
}

// IsCloud reports an App Engine instance.
func (i Info) IsCloud() bool {
	return i.Kind != Local
}

// IsGAECronHeader is true when this HTTP request is App Engine Cron.
func IsGAECronHeader(h map[string][]string) bool {
	if v := headerGet(h, "X-Appengine-Cron"); strings.EqualFold(v, "true") {
		return true
	}
	return false
}

func (i Info) String() string {
	return fmt.Sprintf("runtime=%s service=%q version=%q project=%q", i.Kind, i.Service, i.Version, i.Project)
}

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func headerGet(h map[string][]string, key string) string {
	if h == nil {
		return ""
	}
	if v := h[key]; len(v) > 0 {
		return v[0]
	}
	// http.Header canonical keys
	for k, v := range h {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}
