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
)

// ParsePktLine lee líneas en formato pkt-line del protocolo Git
func ParsePktLine(r io.Reader) ([]byte, error) {
	lenBuf := make([]byte, 4)
	_, err := io.ReadFull(r, lenBuf)
	if err != nil {
		return nil, err
	}

	lenStr := string(lenBuf)
	if lenStr == "0000" {
		return nil, nil // flush packet
	}

	length, err := strconv.ParseInt(lenStr, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid pkt-line length: %s", lenStr)
	}

	// Restar 4 porque la longitud incluye los 4 bytes del prefijo
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

// RefUpdate representa una actualización de referencia
type RefUpdate struct {
	OldSHA string
	NewSHA string
	Ref    string
}

// ExtractPackfile extrae el packfile y la información de actualización de refs
func ExtractPackfile(body []byte) ([]byte, *RefUpdate, error) {
	reader := bytes.NewReader(body)
	var refUpdate *RefUpdate

	// Leer las líneas de comandos (actualizaciones de refs)
	for {
		line, err := ParsePktLine(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, fmt.Errorf("error parsing pkt-line: %v", err)
		}

		// flush packet indica fin de comandos
		if line == nil {
			break
		}

		// Las líneas de comando terminan con \n o \x00
		lineStr := string(line)
		fmt.Printf("DEBUG: Command line: %q\n", lineStr)

		// Parsear comando: old-sha new-sha ref\x00capabilities
		parts := strings.Fields(lineStr)
		if len(parts) >= 3 && refUpdate == nil {
			refUpdate = &RefUpdate{
				OldSHA: parts[0],
				NewSHA: parts[1],
				Ref:    parts[2],
			}
			fmt.Printf("DEBUG: Parsed ref update: %s -> %s for %s\n", refUpdate.OldSHA, refUpdate.NewSHA, refUpdate.Ref)
		}

		// Si encontramos "PACK", retrocedemos porque es el inicio del packfile
		if strings.Contains(lineStr, "PACK") {
			// Retroceder al inicio del PACK
			currentPos, err := reader.Seek(0, io.SeekCurrent)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to determine pack start: %v", err)
			}
			packStart := currentPos - int64(len(line))
			_, err = reader.Seek(packStart, io.SeekStart)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to seek pack start: %v", err)
			}
			break
		}
	}

	// Ahora leer el resto como packfile
	packfile, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, err
	}

	// Verificar que comience con "PACK"
	if len(packfile) < 4 || !bytes.Equal(packfile[:4], []byte("PACK")) {
		// Buscar PACK en todo el body como fallback
		packStart := bytes.Index(body, []byte("PACK"))
		if packStart == -1 {
			return nil, nil, fmt.Errorf("no packfile found in body")
		}
		packfile = body[packStart:]
	}

	fmt.Printf("DEBUG: Extracted packfile: %d bytes, starts with: %x\n",
		len(packfile), packfile[:min(20, len(packfile))])

	return packfile, refUpdate, nil
}

// ReceivePack clona el repo de GitHub y aplica el packfile recibido, retorna el SHA del nuevo commit
func ReceivePack(tempDir string, body []byte, owner string, repo string) (string, error) {
	// Clonar el repo de GitHub para tener los objetos base
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	repoURL := fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, repo)
	fmt.Printf("DEBUG: Cloning %s/%s...\n", owner, repo)

	_, err := git.PlainClone(tempDir, false, &git.CloneOptions{
		URL: repoURL,
	})
	if err != nil {
		// Si falla el clone (repo no existe o privado), inicializar vacío
		fmt.Printf("DEBUG: Clone failed, initializing empty repo: %v\n", err)
		_, err = git.PlainInit(tempDir, false)
		if err != nil {
			return "", fmt.Errorf("failed to init repo: %v", err)
		}
	}

	// Crear directorio pack (necesario para git index-pack)
	packDir := tempDir + "/.git/objects/pack"
	if err := os.MkdirAll(packDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create pack dir: %v", err)
	}

	// Si body está vacío, salir
	if len(body) == 0 {
		return "", nil
	}

	fmt.Printf("DEBUG: Body length: %d bytes\n", len(body))
	fmt.Printf("DEBUG: First 100 bytes: %x\n", body[:min(100, len(body))])

	// Extraer packfile del protocolo Git Smart HTTP
	packfile, refUpdate, err := ExtractPackfile(body)
	if err != nil {
		return "", fmt.Errorf("failed to extract packfile: %v", err)
	}

	if refUpdate == nil {
		return "", fmt.Errorf("no ref update found in request")
	}

	fmt.Printf("DEBUG: Target SHA: %s\n", refUpdate.NewSHA)

	fmt.Printf("DEBUG: Packfile size: %d bytes\n", len(packfile))

	// Guardar packfile temporalmente
	packfilePath := tempDir + "/pack.tmp"
	err = os.WriteFile(packfilePath, packfile, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write packfile: %v", err)
	}

	// Usar git index-pack en lugar de unpack-objects (más robusto)
	cmd := exec.Command("git", "index-pack", "-v", "--stdin", "--fix-thin")
	cmd.Dir = packDir
	cmd.Stdin = bytes.NewReader(packfile)

	output, err := cmd.CombinedOutput()
	fmt.Printf("DEBUG: git index-pack output: %s\n", string(output))

	if err != nil {
		// Si index-pack falla, intentar unpack-objects
		fmt.Printf("DEBUG: index-pack failed, trying unpack-objects\n")
		cmd = exec.Command("git", "unpack-objects", "-r")
		cmd.Dir = tempDir
		cmd.Stdin = bytes.NewReader(packfile)

		output, err = cmd.CombinedOutput()
		fmt.Printf("DEBUG: git unpack-objects output: %s\n", string(output))

		if err != nil {
			return "", fmt.Errorf("failed to unpack objects: %v\nOutput: %s", err, string(output))
		}
	}

	// Abrir repositorio
	r, err := git.PlainOpen(tempDir)
	if err != nil {
		return "", fmt.Errorf("failed to open repo: %v", err)
	}

	// Actualizar HEAD al nuevo commit
	newHash := plumbing.NewHash(refUpdate.NewSHA)
	ref := plumbing.NewHashReference(plumbing.HEAD, newHash)
	err = r.Storer.SetReference(ref)
	if err != nil {
		return "", fmt.Errorf("failed to update HEAD: %v", err)
	}

	fmt.Printf("DEBUG: Updated HEAD to %s\n", refUpdate.NewSHA)

	// Reescribir commits para anonimizar
	anonymizedSHA, err := AnonymizeCommits(r, refUpdate.NewSHA)
	if err != nil {
		return "", fmt.Errorf("failed to anonymize commits: %v", err)
	}

	fmt.Printf("DEBUG: Anonymized commit: %s\n", anonymizedSHA)
	return anonymizedSHA, nil
}

// AnonymizeCommits reescribe solo los commits nuevos para anonimizar autor y committer
func AnonymizeCommits(r *git.Repository, targetSHA string) (string, error) {
	targetHash := plumbing.NewHash(targetSHA)

	// Obtener el commit objetivo
	targetCommit, err := r.CommitObject(targetHash)
	if err != nil {
		return "", fmt.Errorf("failed to get target commit: %v", err)
	}

	// Obtener todos los commits que ya existen en origin/main (commits del repo base)
	baseCommits := make(map[plumbing.Hash]bool)
	originMain, err := r.Reference(plumbing.NewRemoteReferenceName("origin", "main"), true)
	if err == nil {
		// Si existe origin/main, marcar todos sus commits como base
		iter, err := r.Log(&git.LogOptions{From: originMain.Hash()})
		if err == nil {
			iter.ForEach(func(c *object.Commit) error {
				baseCommits[c.Hash] = true
				return nil
			})
		}
	}

	fmt.Printf("DEBUG: Base commits count: %d\n", len(baseCommits))

	// Mapeo de commits originales a anonimizados
	commitMap := make(map[plumbing.Hash]plumbing.Hash)

	// Reescribir commits recursivamente (solo los nuevos)
	newHash, err := rewriteCommit(r, targetCommit, commitMap, baseCommits)
	if err != nil {
		return "", err
	}

	// Actualizar HEAD al nuevo commit anonimizado
	ref := plumbing.NewHashReference(plumbing.HEAD, newHash)
	err = r.Storer.SetReference(ref)
	if err != nil {
		return "", fmt.Errorf("failed to update HEAD to anonymized commit: %v", err)
	}

	return newHash.String(), nil
}

// rewriteCommit reescribe un commit y sus padres recursivamente
func rewriteCommit(r *git.Repository, commit *object.Commit, commitMap map[plumbing.Hash]plumbing.Hash, baseCommits map[plumbing.Hash]bool) (plumbing.Hash, error) {
	// Si ya reescribimos este commit, retornar el hash anonimizado
	if newHash, exists := commitMap[commit.Hash]; exists {
		return newHash, nil
	}

	// Si este commit ya existe en el repo base, no lo reescribimos
	if baseCommits[commit.Hash] {
		fmt.Printf("DEBUG: Skipping base commit %s\n", commit.Hash.String()[:8])
		return commit.Hash, nil
	}

	// Reescribir padres primero
	var newParents []plumbing.Hash
	for _, parentHash := range commit.ParentHashes {
		parentCommit, err := r.CommitObject(parentHash)
		if err != nil {
			// Si el padre no existe, usar el hash original
			newParents = append(newParents, parentHash)
			continue
		}

		newParentHash, err := rewriteCommit(r, parentCommit, commitMap, baseCommits)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		newParents = append(newParents, newParentHash)
	}

	// Crear nuevo commit con información anonimizada
	anonSignature := object.Signature{
		Name:  "gitGost Anonymous",
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

	// Codificar y guardar el nuevo commit
	obj := r.Storer.NewEncodedObject()
	err := newCommit.Encode(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to encode commit: %v", err)
	}

	newHash, err := r.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to store commit: %v", err)
	}

	// Guardar en el mapa
	commitMap[commit.Hash] = newHash

	fmt.Printf("DEBUG: Rewritten commit %s -> %s\n", commit.Hash.String()[:8], newHash.String()[:8])
	return newHash, nil
}
