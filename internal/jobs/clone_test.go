package jobs

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func buildRepo(t *testing.T, n int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := runGit("", "init", "-q", src); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := runGit(src, "checkout", "-q", "-b", "main"); err != nil {
		t.Fatalf("crear main: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := runGit(src, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("c%d", i)); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	return src
}

func withChunk(t *testing.T, n int) {
	t.Helper()
	old := chunkSize
	chunkSize = n
	t.Cleanup(func() { chunkSize = old })
}

func TestRunCloneSharded(t *testing.T) {
	src := buildRepo(t, 250)
	withChunk(t, 100)

	s := testStore(t)
	dest := filepath.Join(t.TempDir(), "dest")
	id, err := s.Create(&Job{Operation: "clone", URL: "file://" + src, Target: dest, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Run(s, id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateCompleted {
		t.Errorf("estado = %s, se esperaba completed", job.State)
	}
	if repoShallow(dest) {
		t.Error("el repo sigue shallow tras completar")
	}
	if n := gitRevCount(dest); n != 250 {
		t.Errorf("commits = %d, se esperaba 250", n)
	}
	out, _ := gitOutput(dest, "branch", "--show-current")
	if strings.TrimSpace(out) != "main" {
		t.Errorf("rama actual = %q, se esperaba main", out)
	}
	if !strings.Contains(job.Progress, "Bloque") {
		t.Errorf("progreso sin reporte de bloques: %q", job.Progress)
	}
}

func TestRunCloneResumesFromCheckpoint(t *testing.T) {
	src := buildRepo(t, 250)
	withChunk(t, 100)

	dest := filepath.Join(t.TempDir(), "dest")
	if err := runGit("", "init", "-q", dest); err != nil {
		t.Fatalf("init dest: %v", err)
	}
	refspec := "+refs/heads/*:refs/remotes/origin/*"
	if err := runGit(dest, "fetch", "--depth=100", "file://"+src, refspec); err != nil {
		t.Fatalf("checkpoint inicial: %v", err)
	}
	if !repoShallow(dest) {
		t.Fatal("el checkpoint debería ser shallow")
	}
	if n := gitRevCount(dest); n != 100 {
		t.Fatalf("checkpoint con %d commits, se esperaba 100", n)
	}

	s := testStore(t)
	id, err := s.Create(&Job{Operation: "clone", URL: "file://" + src, Target: dest, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Run(s, id); err != nil {
		t.Fatalf("Run (resume): %v", err)
	}

	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateCompleted {
		t.Errorf("estado = %s, se esperaba completed", job.State)
	}
	if repoShallow(dest) {
		t.Error("el repo sigue shallow tras el resume")
	}
	if n := gitRevCount(dest); n != 250 {
		t.Errorf("commits tras resume = %d, se esperaba 250", n)
	}
}

func TestRunCloneSingleBlock(t *testing.T) {
	src := buildRepo(t, 3)
	withChunk(t, 500)

	s := testStore(t)
	dest := filepath.Join(t.TempDir(), "dest")
	id, err := s.Create(&Job{Operation: "clone", URL: "file://" + src, Target: dest, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Run(s, id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateCompleted {
		t.Errorf("estado = %s", job.State)
	}
	if repoShallow(dest) {
		t.Error("repo pequeño no debería quedar shallow")
	}
}
