package jobs

import (
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gitgost.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := testStore(t)

	id, err := s.Create(&Job{Operation: "clone", URL: "https://gitgost.fly.dev/v1/gh/foo/bar"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("Create devolvió id 0")
	}

	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateQueued {
		t.Errorf("estado inicial = %s, se esperaba queued", job.State)
	}
	if job.Operation != "clone" || job.URL != "https://gitgost.fly.dev/v1/gh/foo/bar" {
		t.Errorf("job inesperado: %+v", job)
	}
	if job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		t.Error("timestamps vacíos")
	}
}

func TestTransitionsAndList(t *testing.T) {
	s := testStore(t)

	id, err := s.Create(&Job{Operation: "fetch", Args: []string{"origin", "main"}, CWD: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.SetState(id, StateRunning); err != nil {
		t.Fatalf("SetState(running): %v", err)
	}
	if err := s.SetProgress(id, "Receiving objects: 50%"); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	if err := s.SetState(id, StateCompleted); err != nil {
		t.Fatalf("SetState(completed): %v", err)
	}

	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateCompleted {
		t.Errorf("estado = %s, se esperaba completed", job.State)
	}
	if job.Progress != "Receiving objects: 50%" {
		t.Errorf("progress = %q", job.Progress)
	}
	if len(job.Args) != 2 || job.Args[0] != "origin" {
		t.Errorf("args no preservados: %v", job.Args)
	}
	if job.CWD != "/tmp" {
		t.Errorf("cwd no preservado: %q", job.CWD)
	}

	list, err := s.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Errorf("List inesperada: %+v", list)
	}
}

func TestRunFailingJob(t *testing.T) {
	s := testStore(t)

	// URL inexistente (local, determinista): el job debe pasar a failed sin reintentos.
	id, err := s.Create(&Job{
		Operation: "clone",
		URL:       "file:///no/existe/repo",
		Target:    filepath.Join(t.TempDir(), "dest"),
		CWD:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Run(s, id); err == nil {
		t.Fatal("Run debería fallar con URL inexistente")
	}

	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateFailed {
		t.Errorf("estado = %s, se esperaba failed", job.State)
	}
	if job.Error == "" {
		t.Error("error vacío en job fallido")
	}
}

func TestDelete(t *testing.T) {
	s := testStore(t)
	id, err := s.Create(&Job{Operation: "fetch"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); err == nil {
		t.Error("Get tras Delete debería fallar")
	}
}
