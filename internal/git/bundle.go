package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const bundleChunkSize = 500

func CreateBundle(ctx context.Context, url, workDir string) (bundlePath, defaultBranch string, err error) {
	repoDir := filepath.Join(workDir, "repo")
	if err := gitClone(ctx, url, repoDir); err != nil {
		return "", "", fmt.Errorf("clonar %s: %w", url, err)
	}

	block := 0
	for repoShallow(ctx, repoDir) {
		block++
		prevShallow := shallowFile(repoDir)
		prev := revCount(ctx, repoDir)
		if err := runGit(ctx, repoDir, "fetch", "--deepen="+strconv.Itoa(bundleChunkSize), "origin"); err != nil {
			return "", "", fmt.Errorf("profundizar repo (bloque %d): %w", block, err)
		}
		if !repoShallow(ctx, repoDir) {
			break
		}
		if shallowFile(repoDir) == prevShallow && revCount(ctx, repoDir) == prev {
			break
		}
	}
	if repoShallow(ctx, repoDir) {
		if err := runGit(ctx, repoDir, "fetch", "--unshallow", "origin"); err != nil {
			return "", "", fmt.Errorf("completar historia: %w", err)
		}
	}

	if err := runGit(ctx, repoDir, "fetch", "--tags", "origin"); err != nil {
		return "", "", fmt.Errorf("traer tags de origin: %w", err)
	}

	branch, err := gitOutput(ctx, repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("determinar rama por defecto: %w", err)
	}

	bundlePath = filepath.Join(workDir, "repo.bundle")
	if err := runGit(ctx, repoDir, "bundle", "create", bundlePath, "--all"); err != nil {
		return "", "", fmt.Errorf("crear bundle: %w", err)
	}
	return bundlePath, strings.TrimSpace(branch), nil
}

var repoURLPattern = regexp.MustCompile(`(?i)^https://(github\.com|gitlab\.com|codeberg\.org)(?::\d+)?/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/?$`)

func safeCloneURL(raw string) (string, error) {
	if !repoURLPattern.MatchString(raw) {
		return "", fmt.Errorf("URL de repositorio inválida: %q", raw)
	}
	m := repoURLPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("URL de repositorio inválida: %q", raw)
	}
	host := strings.ToLower(m[1])
	owner, repo := m[2], m[3]
	if strings.Contains(owner, "..") || strings.Contains(repo, "..") {
		return "", fmt.Errorf("URL de repositorio inválida: %q", raw)
	}
	return fmt.Sprintf("https://%s/%s/%s", host, owner, repo), nil
}

func shallowFile(dir string) string {
	for _, p := range []string{
		filepath.Join(dir, "shallow"),
		filepath.Join(dir, ".git", "shallow"),
	} {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func repoShallow(ctx context.Context, dir string) bool {
	out, err := gitOutput(ctx, dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

func revCount(ctx context.Context, dir string) int {
	out, err := gitOutput(ctx, dir, "rev-list", "--count", "--all")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func gitClone(ctx context.Context, rawURL, dest string) error {
	safeURL, err := safeCloneURL(rawURL)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git",
		"clone",
		"--mirror",
		"--depth="+strconv.Itoa(bundleChunkSize),
		"--",
		safeURL,
		dest,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}
