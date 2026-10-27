package config

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/tokensync"
)

const (
	// DefaultProject is the one GCP project this system runs in. It is only the
	// fallback for finding the config bucket; GOOGLE_CLOUD_PROJECT (set by App Engine)
	// wins.
	DefaultProject = "trade-orchestrator-508722"
	// ConfigObject is where the whole configuration lives: one KEY=VALUE object in the
	// data bucket. It replaces Secret Manager, app.yaml env_variables and .env.
	ConfigObject = "config/env"
)

// Project is the GCP project: GOOGLE_CLOUD_PROJECT, App Engine's
// GAE_APPLICATION ("e~project"), else DefaultProject.
func Project() string {
	for _, k := range []string{"GOOGLE_CLOUD_PROJECT", "GAE_APPLICATION"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			if i := strings.IndexByte(v, '~'); i >= 0 {
				v = v[i+1:]
			}
			return v
		}
	}
	return DefaultProject
}

// Bucket is the data bucket: GCS_BUCKET when set, else trade-orchestrator-data-<project>.
func Bucket() string {
	if v := strings.TrimSpace(os.Getenv("GCS_BUCKET")); v != "" {
		return v
	}
	return "trade-orchestrator-data-" + Project()
}

// ParseEnv reads KEY=VALUE lines (the .env format): blank lines and # comments are
// skipped, a line without "=" is ignored, and one pair of surrounding quotes is removed.
func ParseEnv(data []byte) (keys []string, values map[string]string) {
	values = map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if k == "" {
			continue
		}
		if _, dup := values[k]; !dup {
			keys = append(keys, k)
		}
		values[k] = v
	}
	return keys, values
}

// Bootstrap fills the process environment from gs://<bucket>/config/env. It is the
// only place configuration comes from: no Secret Manager, no app.yaml env_variables,
// no .env. A variable already set in the environment wins (a shell override, or one
// the runtime set such as GAE_*), so the
// object supplies defaults for everything else. It also sets GOOGLE_CLOUD_PROJECT and
// GCS_BUCKET when unset, and TZ as the process's local time zone.
//
// Every binary's main() calls it first, before anything reads the environment. It
// returns the names of the variables it set (never their values). It reads GCS, so it
// has no automated test; ParseEnv, Project and Bucket do.
func Bootstrap(ctx context.Context) ([]string, error) {
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" {
		os.Setenv("GOOGLE_CLOUD_PROJECT", Project())
	}
	bucket := Bucket()
	if os.Getenv("GCS_BUCKET") == "" {
		os.Setenv("GCS_BUCKET", bucket)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, err := tokensync.GetObject(ctx, bucket, ConfigObject)
	if err != nil {
		return nil, fmt.Errorf("configuration is read from gs://%s/%s (Secret Manager, app.yaml env and .env are no longer used): %w. "+
			"Locally, run `gcloud auth application-default login`. To create the object, run deploy_gae/config.sh migrate", bucket, ConfigObject, err)
	}
	keys, values := ParseEnv(data)
	var set []string
	for _, k := range keys {
		if os.Getenv(k) == "" {
			os.Setenv(k, values[k])
			set = append(set, k)
		}
	}
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			time.Local = loc
		}
	}
	return set, nil
}
