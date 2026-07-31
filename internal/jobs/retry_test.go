package jobs

import (
	"errors"
	"testing"
)

func TestIsRetryable(t *testing.T) {
	retryable := []string{
		"fatal: unable to access 'https://x/': Could not resolve host: github.com",
		"fatal: unable to access 'https://x/': Failed to connect to github.com port 443: Connection refused",
		"error: RPC failed; HTTP 502 curl 22 The requested URL returned error: 502",
		"fatal: early EOF",
		"fatal: the remote end hung up unexpectedly",
		"fatal: fetch-pack: invalid index-pack output",
		"error: connection reset by peer",
		"fatal: unable to access: The requested URL returned error: 504",
		"fatal: Operation timed out after 30000 milliseconds",
	}
	for _, msg := range retryable {
		if !isRetryable(errors.New(msg)) {
			t.Errorf("debería ser retryable: %s", msg)
		}
	}

	notRetryable := []string{
		"fatal: repository 'https://github.com/no/such' not found",
		"remote: Repository not found.",
		"fatal: '/no/existe' does not appear to be a git repository",
		"remote: Authentication failed",
		"fatal: destination path 'repo' already exists and is not an empty directory",
		"fatal: couldn't find remote ref refs/heads/nope",
		"error: Permission denied (publickey)",
	}
	for _, msg := range notRetryable {
		if isRetryable(errors.New(msg)) {
			t.Errorf("no debería ser retryable: %s", msg)
		}
	}
}

func TestParseDefaultBranch(t *testing.T) {
	ls := "ref: refs/heads/main\tHEAD\nabc123\tHEAD\n"
	if got := parseDefaultBranch(ls); got != "main" {
		t.Errorf("parseDefaultBranch = %q, se esperaba main", got)
	}
	if got := parseDefaultBranch("abc123\tHEAD\n"); got != "" {
		t.Errorf("parseDefaultBranch sin symref = %q, se esperaba vacío", got)
	}
}
