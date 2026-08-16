package git

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

var debugEnabled = os.Getenv("GITGOST_DEBUG") == "1"

func debugf(format string, args ...interface{}) {
	if debugEnabled {
		fmt.Printf(format, args...)
	}
}

func ParsePktLine(r io.Reader) ([]byte, error) {
	lenBuf := make([]byte, 4)
	_, err := io.ReadFull(r, lenBuf)
	if err != nil {
		return nil, err
	}

	lenStr := string(lenBuf)
	if lenStr == "0000" {
		return nil, nil
	}

	length, err := strconv.ParseInt(lenStr, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid pkt-line length: %s", lenStr)
	}

	dataLen := int(length) - 4
	if dataLen < 0 {
		return nil, fmt.Errorf("invalid pkt-line length: %d", length)
	}

	data := make([]byte, dataLen)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

type RefUpdate struct {
	OldSHA string
	NewSHA string
	Ref    string
}

func ExtractPackfile(body []byte) ([]byte, *RefUpdate, string, string, error) {
	reader := bytes.NewReader(body)
	var refUpdate *RefUpdate
	var prHash string
	var githubToken string

	for {
		line, err := ParsePktLine(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, "", "", fmt.Errorf("error parsing pkt-line: %v", err)
		}

		if line == nil {
			break
		}

		lineStr := string(line)
		safeLine := lineStr
		if strings.HasPrefix(lineStr, "push-option=") {
			if idx := strings.Index(lineStr, "github-token="); idx >= 0 {
				safeLine = lineStr[:idx+len("github-token=")] + "<redacted>"
			} else if idx := strings.Index(lineStr, "token="); idx >= 0 {
				safeLine = lineStr[:idx+len("token=")] + "<redacted>"
			}
		}
		debugf("DEBUG: Command line: %q\n", safeLine)

		if strings.HasPrefix(lineStr, "push-option=pr-hash=") {
			prHash = strings.TrimPrefix(lineStr, "push-option=pr-hash=")
			prHash = strings.TrimRight(prHash, "\n")
			debugf("DEBUG: Found pr-hash push-option: %s\n", prHash)
			continue
		}
		if strings.HasPrefix(lineStr, "push-option=github-token=") {
			githubToken = strings.TrimPrefix(lineStr, "push-option=github-token=")
			githubToken = strings.TrimRight(githubToken, "\n")
			debugf("DEBUG: Found github-token push-option\n")
			continue
		}

		parts := strings.Fields(lineStr)
		if len(parts) >= 3 && refUpdate == nil {
			refUpdate = &RefUpdate{
				OldSHA: parts[0],
				NewSHA: parts[1],
				Ref:    parts[2],
			}
			debugf("DEBUG: Parsed ref update: %s -> %s for %s\n", refUpdate.OldSHA, refUpdate.NewSHA, refUpdate.Ref)
		}

		if strings.Contains(lineStr, "PACK") {
			currentPos, err := reader.Seek(0, io.SeekCurrent)
			if err != nil {
				return nil, nil, "", "", fmt.Errorf("failed to determine pack start: %v", err)
			}
			packStart := currentPos - int64(len(line))
			_, err = reader.Seek(packStart, io.SeekStart)
			if err != nil {
				return nil, nil, "", "", fmt.Errorf("failed to seek pack start: %v", err)
			}
			break
		}
	}

	packfile, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, "", "", err
	}

	if len(packfile) < 4 || !bytes.Equal(packfile[:4], []byte("PACK")) {
		packStart := bytes.Index(body, []byte("PACK"))
		if packStart == -1 {
			return nil, nil, "", "", fmt.Errorf("no packfile found in body")
		}
		packfile = body[packStart:]
	}

	debugf("DEBUG: Extracted packfile: %d bytes, starts with: %x\n",
		len(packfile), packfile[:min(20, len(packfile))])

	return packfile, refUpdate, prHash, githubToken, nil
}

func ReceivePack(tempDir string, body []byte, owner string, repo string, cloneURL string, tokenEnvVar string, tokenOverride string) (string, string, string, string, error) {
	if cloneURL == "" {
		cloneURL = fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	}
	if tokenEnvVar == "" {
		tokenEnvVar = "GITHUB_TOKEN"
	}
	if len(body) == 0 {
		return "", "", "", "", nil
	}

	packfile, refUpdate, prHash, githubToken, err := ExtractPackfile(body)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to extract packfile: %v", err)
	}

	token := strings.TrimSpace(tokenOverride)
	if token == "" {
		token = githubToken
	}
	if token == "" {
		token = os.Getenv(tokenEnvVar)
	}
	if token == "" {
		return "", "", "", "", fmt.Errorf("%s not set", tokenEnvVar)
	}

	repoURL := cloneURL
	debugf("DEBUG: Cloning %s/%s...\n", owner, repo)

	_, err = git.PlainClone(tempDir, false, &git.CloneOptions{
		URL: repoURL,
		Auth: &http.BasicAuth{
			Username: "x-access-token",
			Password: token,
		},
	})
	if err != nil {
		debugf("DEBUG: Clone failed, initializing empty repo: %v\n", err)
		_, err = git.PlainInit(tempDir, false)
		if err != nil {
			return "", "", "", "", fmt.Errorf("failed to init repo: %v", err)
		}
	}

	packDir := tempDir + "/.git/objects/pack"
	if err := os.MkdirAll(packDir, 0755); err != nil {
		return "", "", "", "", fmt.Errorf("failed to create pack dir: %v", err)
	}

	debugf("DEBUG: Body length: %d bytes\n", len(body))
	debugf("DEBUG: First 100 bytes: %x\n", body[:min(100, len(body))])

	if refUpdate == nil {
		return "", "", "", "", fmt.Errorf("no ref update found in request")
	}

	debugf("DEBUG: Target SHA: %s\n", refUpdate.NewSHA)

	debugf("DEBUG: Packfile size: %d bytes\n", len(packfile))

	packfilePath := tempDir + "/pack.tmp"
	err = os.WriteFile(packfilePath, packfile, 0644)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to write packfile: %v", err)
	}

	cmd := exec.Command("git", "index-pack", "-v", "--stdin", "--fix-thin")
	cmd.Dir = packDir
	cmd.Stdin = bytes.NewReader(packfile)

	output, err := cmd.CombinedOutput()
	debugf("DEBUG: git index-pack output: %s\n", string(output))

	if err != nil {
		debugf("DEBUG: index-pack failed, trying unpack-objects\n")
		cmd = exec.Command("git", "unpack-objects", "-r")
		cmd.Dir = tempDir
		cmd.Stdin = bytes.NewReader(packfile)

		output, err = cmd.CombinedOutput()
		debugf("DEBUG: git unpack-objects output: %s\n", string(output))

		if err != nil {
			return "", "", "", "", fmt.Errorf("failed to unpack objects: %v\nOutput: %s", err, string(output))
		}
	}

	r, err := git.PlainOpen(tempDir)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to open repo: %v", err)
	}

	newHash := plumbing.NewHash(refUpdate.NewSHA)
	ref := plumbing.NewHashReference(plumbing.HEAD, newHash)
	err = r.Storer.SetReference(ref)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to update HEAD: %v", err)
	}

	debugf("DEBUG: Updated HEAD to %s\n", refUpdate.NewSHA)

	originalCommit, err := r.CommitObject(newHash)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to get original commit: %v", err)
	}
	commitMessage := originalCommit.Message
	debugf("DEBUG: Original commit message: %s\n", commitMessage)

	anonymizedSHA, err := AnonymizeCommits(r, refUpdate.NewSHA)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to anonymize commits: %v", err)
	}

	debugf("DEBUG: Anonymized commit: %s\n", anonymizedSHA)
	return anonymizedSHA, commitMessage, prHash, githubToken, nil
}

func resolveBaseReference(r *git.Repository) *plumbing.Reference {
	if ref, err := r.Reference(plumbing.NewRemoteReferenceName("origin", "HEAD"), true); err == nil {
		return ref
	}

	if head, err := r.Reference(plumbing.HEAD, false); err == nil {
		if head.Type() == plumbing.SymbolicReference {
			target := head.Target()
			if strings.HasPrefix(string(target), "refs/remotes/origin/") {
				if ref, err := r.Reference(target, true); err == nil {
					return ref
				}
			}
			if strings.HasPrefix(string(target), "refs/heads/") {
				if ref, err := r.Reference(target, true); err == nil {
					return ref
				}
				branch := strings.TrimPrefix(string(target), "refs/heads/")
				if ref, err := r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true); err == nil {
					return ref
				}
			}
		}
	}

	if originHead, err := r.Reference(plumbing.ReferenceName("refs/remotes/origin/HEAD"), false); err == nil {
		if originHead.Type() == plumbing.SymbolicReference {
			if ref, err := r.Reference(originHead.Target(), true); err == nil {
				return ref
			}
		}
	}

	if ref, err := r.Reference(plumbing.NewRemoteReferenceName("origin", "main"), true); err == nil {
		return ref
	}
	return nil
}

func AnonymizeCommits(r *git.Repository, targetSHA string) (string, error) {
	targetHash := plumbing.NewHash(targetSHA)

	targetCommit, err := r.CommitObject(targetHash)
	if err != nil {
		return "", fmt.Errorf("failed to get target commit: %v", err)
	}

	baseCommits := make(map[plumbing.Hash]bool)
	baseRef := resolveBaseReference(r)
	if baseRef != nil {
		iter, err := r.Log(&git.LogOptions{From: baseRef.Hash()})
		if err == nil {
			iter.ForEach(func(c *object.Commit) error {
				baseCommits[c.Hash] = true
				return nil
			})
		}
	}

	debugf("DEBUG: Base commits count: %d\n", len(baseCommits))

	commitMap := make(map[plumbing.Hash]plumbing.Hash)

	newHash, err := rewriteCommit(r, targetCommit, commitMap, baseCommits)
	if err != nil {
		return "", err
	}

	ref := plumbing.NewHashReference(plumbing.HEAD, newHash)
	err = r.Storer.SetReference(ref)
	if err != nil {
		return "", fmt.Errorf("failed to update HEAD to anonymized commit: %v", err)
	}

	return newHash.String(), nil
}

func rewriteCommit(r *git.Repository, commit *object.Commit, commitMap map[plumbing.Hash]plumbing.Hash, baseCommits map[plumbing.Hash]bool) (plumbing.Hash, error) {
	if newHash, exists := commitMap[commit.Hash]; exists {
		return newHash, nil
	}

	if baseCommits[commit.Hash] {
		debugf("DEBUG: Skipping base commit %s\n", commit.Hash.String()[:8])
		return commit.Hash, nil
	}

	var newParents []plumbing.Hash
	for _, parentHash := range commit.ParentHashes {
		parentCommit, err := r.CommitObject(parentHash)
		if err != nil {
			newParents = append(newParents, parentHash)
			continue
		}

		newParentHash, err := rewriteCommit(r, parentCommit, commitMap, baseCommits)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		newParents = append(newParents, newParentHash)
	}

	anonSignature := object.Signature{
		Name:  "@gitgost-anonymous",
		Email: "anonymous@gitgost.local",
		When:  time.Now(),
	}

	newCommit := &object.Commit{
		Author:       anonSignature,
		Committer:    anonSignature,
		Message:      commit.Message,
		TreeHash:     commit.TreeHash,
		ParentHashes: newParents,
	}

	obj := r.Storer.NewEncodedObject()
	err := newCommit.Encode(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to encode commit: %v", err)
	}

	newHash, err := r.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to store commit: %v", err)
	}

	commitMap[commit.Hash] = newHash

	debugf("DEBUG: Rewritten commit %s -> %s\n", commit.Hash.String()[:8], newHash.String()[:8])
	return newHash, nil
}
