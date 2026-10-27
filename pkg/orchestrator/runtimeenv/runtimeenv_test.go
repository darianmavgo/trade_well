package runtimeenv

import (
	"os"
	"testing"
)

func TestDetectLocal(t *testing.T) {
	clearCloudEnv(t)
	got := Detect()
	if got.Kind != Local {
		t.Fatalf("got %s want local", got.Kind)
	}
}

func TestDetectGAE(t *testing.T) {
	clearCloudEnv(t)
	t.Setenv("GAE_ENV", "standard")
	t.Setenv("GAE_SERVICE", "default")
	got := Detect()
	if got.Kind != GAE {
		t.Fatalf("got %s want gae", got.Kind)
	}
	if !got.ShouldAutoServe() {
		t.Fatal("GAE should auto-serve")
	}
}

func TestIsGAECronHeader(t *testing.T) {
	if !IsGAECronHeader(map[string][]string{"X-Appengine-Cron": {"true"}}) {
		t.Fatal("expected cron header")
	}
	if IsGAECronHeader(map[string][]string{}) {
		t.Fatal("empty should be false")
	}
	// The old hardcoded bypass header must never authorize a cron route.
	if IsGAECronHeader(map[string][]string{"X-Cron-Auth": {"gae-cron"}}) {
		t.Fatal("X-Cron-Auth must not be accepted")
	}
}

func clearCloudEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GAE_ENV", "GAE_INSTANCE", "GAE_SERVICE", "GAE_RUNTIME",
		"GAE_APPLICATION", "GAE_VERSION", "GOOGLE_CLOUD_PROJECT",
	} {
		_ = os.Unsetenv(k)
	}
}
