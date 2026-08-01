package cli

import "testing"

func TestRewriteURL(t *testing.T) {
	base := "https://gitgost.fly.dev"
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"github https", "https://github.com/openai/openai.git", "https://gitgost.fly.dev/v1/gh/openai/openai"},
		{"github sin .git", "https://github.com/livrasand/gitGost", "https://gitgost.fly.dev/v1/gh/livrasand/gitGost"},
		{"github ssh scp-like", "git@github.com:torvalds/linux.git", "https://gitgost.fly.dev/v1/gh/torvalds/linux"},
		{"gitlab", "https://gitlab.com/group/repo.git", "https://gitgost.fly.dev/v1/gl/group/repo"},
		{"codeberg", "https://codeberg.org/user/repo.git", "https://gitgost.fly.dev/v1/cb/user/repo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RewriteURL(base, tc.raw)
			if err != nil {
				t.Fatalf("RewriteURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("RewriteURL(%q) = %q, se esperaba %q", tc.raw, got, tc.want)
			}
		})
	}

	if got, err := RewriteURL("https://gitgost.fly.dev/", "https://github.com/foo/bar"); err != nil || got != "https://gitgost.fly.dev/v1/gh/foo/bar" {
		t.Errorf("base con slash final: got=%q err=%v", got, err)
	}
}

func TestRewriteURLInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"host no soportado", "https://bitbucket.org/user/repo.git"},
		{"sin path", "https://github.com"},
		{"path con un solo segmento", "https://github.com/solo"},
		{"path con tres segmentos", "https://github.com/a/b/c"},
		{"scp mal formado", "git@github.com"},
		{"garbage", "no es una url"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RewriteURL("https://gitgost.fly.dev", tc.raw); err == nil {
				t.Errorf("RewriteURL(%q) debería fallar", tc.raw)
			}
		})
	}
}
