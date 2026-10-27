package cronclient

import (
	"context"
	"testing"
)

func TestTriggerRejectsBadJobNames(t *testing.T) {
	for _, j := range []string{"", "a/b", "scan market", "x?y=1"} {
		if _, err := Trigger(context.Background(), j, Options{BaseURL: "http://x", Token: "t"}); err == nil {
			t.Errorf("job %q should be rejected", j)
		}
	}
}

func TestBaseURLResolution(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")

	t.Setenv(EnvURL, "https://a.example.com/")
	if u, _ := BaseURL(ctx); u != "https://a.example.com" {
		t.Fatalf("explicit URL: got %q", u)
	}

	t.Setenv(EnvURL, "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")
	if u, _ := BaseURL(ctx); u != "https://env-proj.uc.r.appspot.com" {
		t.Fatalf("env project: got %q", u)
	}

}

// The gcloud fallback in BaseURL runs the real gcloud, so only its output parsing is tested.
func TestGcloudProject(t *testing.T) {
	for out, want := range map[string]struct {
		p  string
		ok bool
	}{
		"gcloud-proj\n": {"gcloud-proj", true},
		"(unset)\n":     {"(unset)", false},
		"":              {"", false},
	} {
		if p, ok := gcloudProject(out); p != want.p || ok != want.ok {
			t.Errorf("gcloudProject(%q) = %q, %v", out, p, ok)
		}
	}
}
