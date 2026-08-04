package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// Shipping templates must not re-enable query identity (N-06).
func TestShippingTemplatesKeepQueryIdentityOff(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	enabled := regexp.MustCompile(`ALLOW_QUERY_USER_ID["=,\s]+true`)
	files := []string{
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "docker-compose.scale.yml"),
		filepath.Join(root, "deploy", "terraform", "ecs.tf"),
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if enabled.Match(body) {
			t.Errorf("%s enables ALLOW_QUERY_USER_ID=true; shipping templates must stay false", path)
		}
	}
}
