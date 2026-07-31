package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/jobs"
)

// version del CLI; puede inyectarse con -ldflags en el build.
var version = "0.1.0"

// Run despacha el subcomando de `git gost ...`. Devuelve el código de salida.
func Run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 0
	}

	switch args[0] {
	case "install":
		return cmdInstall()
	case "clone":
		return cmdClone(args[1:])
	case "fetch":
		return cmdGitJob("fetch", args[1:])
	case "pull":
		return cmdGitJob("pull", args[1:])
	case "push":
		return cmdGitJob("push", args[1:])
	case "jobs":
		return cmdJobs(args[1:])
	case "watch":
		return cmdWatch(args[1:])
	case "pause":
		return cmdPause(args[1:])
	case "resume":
		return cmdResume(args[1:])
	case "cancel":
		return cmdCancel(args[1:])
	case "run": // interno: ejecuta un job en background
		return cmdRun(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	case "version", "--version", "-v":
		fmt.Printf("git-gost %s\n", version)
		return 0
	default:
		// Pass-through: cualquier comando Git sin lógica especial.
		return passthrough(args)
	}
}

// passthrough reenvía el comando a Git (status, log, branch, diff, ...).
func passthrough(args []string) int {
	cmd := exec.Command("git", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	return 0
}

// dataDir devuelve el directorio de datos del cliente (env GITGOST_HOME o ~/.gitgost).
func dataDir() string {
	if v := os.Getenv("GITGOST_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gitgost"
	}
	return filepath.Join(home, ".gitgost")
}

func dbPath() string {
	return filepath.Join(dataDir(), "gitgost.db")
}

func openStore() (*jobs.Store, error) {
	return jobs.Open(dbPath())
}

// cmdInstall prepara el entorno del cliente: crea la cola SQLite y, si el
// binario no está disponible como 'git-gost' en el PATH, se auto-instala en
// ~/.local/bin y añade ese directorio al PATH del shell.
func cmdInstall() int {
	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: instalación fallida: %v\n", err)
		return 1
	}
	s.Close()

	if err := selfInstall(); err != nil {
		fmt.Fprintf(os.Stderr, "aviso: %v\n", err)
	}

	fmt.Println("git gost install: OK")
	fmt.Printf("Cola de jobs: %s\n", dbPath())
	fmt.Println("Uso: git gost clone <url>  |  git gost jobs  |  git gost watch <id>")
	return 0
}

// selfInstall copia el binario en ejecución como 'git-gost' a ~/.local/bin y,
// en Unix, añade ese directorio al PATH del shell si no está ya. No hace nada
// si ya hay un 'git-gost' disponible en el PATH.
func selfInstall() error {
	if _, err := exec.LookPath("git-gost"); err == nil {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("localizar el binario en ejecución: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("localizar el directorio home: %w", err)
	}

	binName := "git-gost"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binDir := filepath.Join(home, ".local", "bin")
	target := filepath.Join(binDir, binName)

	if exe != target {
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return fmt.Errorf("crear %s: %w", binDir, err)
		}
		if err := copyFile(exe, target); err != nil {
			return fmt.Errorf("copiar binario a %s: %w", target, err)
		}
		if err := os.Chmod(target, 0o755); err != nil {
			return fmt.Errorf("dar permisos a %s: %w", target, err)
		}
		fmt.Printf("git-gost instalado en %s\n", target)
	}

	if runtime.GOOS == "windows" {
		// En Windows no se editan rc files: se indica el directorio a añadir.
		fmt.Printf("Añade %s a tu PATH para usar 'git gost ...'.\n", binDir)
		return nil
	}
	return addToPath(binDir, home)
}

// addToPath añade dir al PATH del shell del usuario si no está ya referenciado.
func addToPath(dir, home string) error {
	shell := filepath.Base(os.Getenv("SHELL"))
	var rc, line string
	switch shell {
	case "fish":
		rc = filepath.Join(home, ".config", "fish", "config.fish")
		line = fmt.Sprintf("fish_add_path %q", dir)
	case "bash":
		rc = filepath.Join(home, ".bashrc")
		if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
			rc = filepath.Join(home, ".bash_profile")
		}
		line = fmt.Sprintf(`export PATH=%q:$PATH`, dir)
	default: // zsh y cualquier otro shell
		rc = filepath.Join(home, ".zshrc")
		line = fmt.Sprintf(`export PATH=%q:$PATH`, dir)
	}

	data, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), dir) {
		return nil // el directorio ya está en el PATH del shell
	}

	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		return fmt.Errorf("crear %s: %w", filepath.Dir(rc), err)
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n# git-gost\n%s\n", line); err != nil {
		return err
	}
	fmt.Printf("Añadido %s al PATH en %s (recarga tu shell o ejecuta: source %s)\n", dir, rc, rc)
	return nil
}

// copyFile copia un archivo (los permisos se ajustan aparte).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// cmdClone crea un job de clone. Por defecto corre en background; con -f/--foreground en primer plano.
func cmdClone(args []string) int {
	foreground, rest, err := parseFlags(args)
	if err != nil {
		return 1
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "uso: git gost clone <url> [directorio] [-f]")
		return 1
	}
	url, target := rest[0], ""
	if len(rest) > 1 {
		target = rest[1]
	}
	if target == "" {
		target = defaultCloneDir(url)
	}

	rewritten, err := RewriteURL(ServerBase(), url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}

	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	defer s.Close()

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	id, err := s.Create(&jobs.Job{
		Operation: "clone",
		URL:       rewritten,
		Origin:    url,
		Target:    target,
		CWD:       cwd,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: crear job: %v\n", err)
		return 1
	}

	if foreground {
		return runForeground(s, id)
	}
	return launchBackground(s, id)
}

// defaultCloneDir deriva el directorio destino de un clone a partir de la URL
// original (basename del repo, sin .git), igual que hace `git clone`.
func defaultCloneDir(raw string) string {
	u, err := parseRepoURL(raw)
	if err != nil {
		return "repo"
	}
	parts := splitPath(u.Path)
	if len(parts) == 0 {
		return "repo"
	}
	return strings.TrimSuffix(parts[len(parts)-1], ".git")
}

// cmdGitJob crea un job para fetch/pull/push en el repositorio actual.
func cmdGitJob(op string, args []string) int {
	foreground, rest, err := parseFlags(args)
	if err != nil {
		return 1
	}

	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	defer s.Close()

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	id, err := s.Create(&jobs.Job{
		Operation: op,
		Args:      rest,
		CWD:       cwd,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: crear job: %v\n", err)
		return 1
	}

	if foreground {
		return runForeground(s, id)
	}
	return launchBackground(s, id)
}

// parseFlags extrae -f/--foreground del resto de argumentos.
func parseFlags(args []string) (foreground bool, rest []string, err error) {
	for _, a := range args {
		switch a {
		case "-f", "--foreground":
			foreground = true
		default:
			rest = append(rest, a)
		}
	}
	return foreground, rest, nil
}

// runForeground ejecuta el job en primer plano y muestra el resultado.
func runForeground(s *jobs.Store, id int64) int {
	if err := jobs.Run(s, id); err != nil {
		job, gerr := s.Get(id)
		if gerr == nil && job.Error != "" {
			fmt.Fprintf(os.Stderr, "git-gost: %s\n", job.Error)
		} else {
			fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		}
		return 1
	}
	fmt.Printf("Job %d: completed\n", id)
	return 0
}

// launchBackground lanza `git-gost run <id>` como proceso independiente.
func launchBackground(s *jobs.Store, id int64) int {
	pid, err := startBackground(id)
	if err != nil {
		_ = s.SetState(id, jobs.StateFailed)
		_ = s.SetError(id, err.Error())
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	_ = s.SetPID(id, pid)
	fmt.Printf("Job created.\n\nID: %d\nRunning in background...\n\nUse 'git gost watch %d' to follow progress.\n", id, id)
	return 0
}

// cmdRun ejecuta un job de la cola (invocado como proceso hijo en background).
func cmdRun(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "uso interno: git gost run <id>")
		return 1
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: id inválido: %s\n", args[0])
		return 1
	}
	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	defer s.Close()
	if err := jobs.Run(s, id); err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	return 0
}

// cmdJobs lista los jobs recientes de la cola.
func cmdJobs(args []string) int {
	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	defer s.Close()

	limit := 50
	for _, a := range args {
		if n, err := strconv.Atoi(a); err == nil && n > 0 {
			limit = n
		}
	}

	list, err := s.List(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}

	fmt.Printf("%-6s %-10s %-10s %s\n", "ID", "STATUS", "OPERATION", "TARGET")
	for _, j := range list {
		target := j.Target
		if target == "" {
			if j.URL != "" {
				target = j.URL
			} else {
				target = "."
			}
		}
		fmt.Printf("%-6d %-10s %-10s %s\n", j.ID, strings.ToUpper(j.State), j.Operation, target)
	}
	return 0
}

// cmdWatch sigue el progreso de un job hasta que termina.
func cmdWatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "uso: git gost watch <id>")
		return 1
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: id inválido: %s\n", args[0])
		return 1
	}
	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	defer s.Close()

	last := ""
	for {
		job, err := s.Get(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
			return 1
		}
		if job.Progress != last {
			fmt.Printf("[%s] %s\n", strings.ToUpper(job.State), job.Progress)
			last = job.Progress
		}
		if job.State == jobs.StateCompleted || job.State == jobs.StateFailed {
			if job.State == jobs.StateFailed && job.Error != "" {
				fmt.Printf("[FAILED] %s\n", job.Error)
			} else {
				fmt.Printf("[COMPLETED] Job %d terminado\n", id)
			}
			return 0
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// cmdPause pausa un job en cola o en ejecución.
func cmdPause(args []string) int {
	return signalCommand("pause", args, func(j *jobs.Job, s *jobs.Store) (int, error) {
		switch j.State {
		case jobs.StateQueued, jobs.StateRetrying:
			return 0, s.SetState(j.ID, jobs.StatePaused)
		case jobs.StateRunning:
			if err := signalJob(j.PID, sigStop); err != nil {
				return 0, err
			}
			return 0, s.SetState(j.ID, jobs.StatePaused)
		default:
			return 0, fmt.Errorf("el job %d no se puede pausar (estado: %s)", j.ID, j.State)
		}
	})
}

// cmdResume reanuda un job pausado.
func cmdResume(args []string) int {
	return signalCommand("resume", args, func(j *jobs.Job, s *jobs.Store) (int, error) {
		if j.State != jobs.StatePaused {
			return 0, fmt.Errorf("el job %d no está pausado (estado: %s)", j.ID, j.State)
		}
		if j.PID > 0 {
			if err := signalJob(j.PID, sigCont); err != nil {
				return 0, err
			}
			return 0, s.SetState(j.ID, jobs.StateRunning)
		}
		// Pausado sin proceso (estaba en cola): relanzar en background.
		pid, err := startBackground(j.ID)
		if err != nil {
			return 0, err
		}
		_ = s.SetPID(j.ID, pid)
		return 0, s.SetState(j.ID, jobs.StateQueued)
	})
}

// cmdCancel cancela un job pendiente o en ejecución.
func cmdCancel(args []string) int {
	return signalCommand("cancel", args, func(j *jobs.Job, s *jobs.Store) (int, error) {
		switch j.State {
		case jobs.StateQueued, jobs.StateRetrying, jobs.StatePaused:
			_ = s.SetState(j.ID, jobs.StateFailed)
			return 0, s.SetError(j.ID, "cancelled")
		case jobs.StateRunning:
			_ = signalJob(j.PID, sigTerm)
			_ = s.SetState(j.ID, jobs.StateFailed)
			return 0, s.SetError(j.ID, "cancelled")
		default:
			return 0, fmt.Errorf("el job %d ya terminó (%s)", j.ID, j.State)
		}
	})
}

// signalCommand resuelve el id, aplica la acción y reporta el resultado.
func signalCommand(name string, args []string, action func(*jobs.Job, *jobs.Store) (int, error)) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "uso: git gost %s <id>\n", name)
		return 1
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: id inválido: %s\n", args[0])
		return 1
	}
	s, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	defer s.Close()

	j, err := s.Get(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	if _, err := action(j, s); err != nil {
		fmt.Fprintf(os.Stderr, "git-gost: %v\n", err)
		return 1
	}
	fmt.Printf("Job %d: %s\n", id, name)
	return 0
}

func printUsage() {
	fmt.Print(`git-gost: una capa de red resiliente para Git (Fase 0).

Uso:
  git gost clone <url> [directorio]   Clonar vía servidor gitGost (background por defecto)
  git gost fetch [args...]            Crear job de fetch en el repo actual
  git gost pull [args...]             Crear job de pull en el repo actual
  git gost push [args...]             Crear job de push en el repo actual
  git gost jobs [n]                   Listar los últimos n jobs (por defecto 50)
  git gost watch <id>                 Seguir el progreso de un job
  git gost pause <id>                 Pausar un job
  git gost resume <id>                Reanudar un job
  git gost cancel <id>                Cancelar un job
  git gost install                    Preparar el entorno del cliente
  git gost version                    Mostrar versión

Cualquier otro comando (status, log, branch, ...) se reenvía a Git.

Opciones de jobs:
  -f, --foreground   Ejecutar en primer plano mostrando el progreso en vivo

Variables de entorno:
  GITGOST_SERVER   URL base del servidor gitGost (por defecto https://gitgost.fly.dev)
  GITGOST_HOME     Directorio de datos (por defecto ~/.gitgost)
`)
}
