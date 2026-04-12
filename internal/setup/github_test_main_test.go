package setup

import (
	"context"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	originalLookup := LookupGitIdentityFunc
	LookupGitIdentityFunc = func(ctx context.Context) (GitIdentity, error) {
		return GitIdentity{Name: "Test User", Email: "test@example.com"}, nil
	}

	code := m.Run()

	LookupGitIdentityFunc = originalLookup
	os.Exit(code)
}
