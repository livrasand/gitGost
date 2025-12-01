package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/go-git/go-git/v5"
)

func ReceivePack(tempDir string, body []byte) error {
	// Initialize repo
	_, err := git.PlainInit(tempDir, false)
	if err != nil {
		return err
	}

	// If body is empty, skip unpacking
	if len(body) == 0 {
		return nil
	}

	// Parse Git Smart HTTP receive-pack protocol
	// Body contains pkt-lines with commands, then flush (0000), then packfile
	packfile, err := parseReceivePackBody(body)
	if err != nil {
		return fmt.Errorf("failed to parse receive-pack body: %w", err)
	}

	if len(packfile) == 0 {
		// No packfile, just commands (e.g., delete refs)
		return nil
	}

	// Use git unpack-objects to unpack the packfile
	cmd := exec.Command("git", "unpack-objects")
	cmd.Dir = tempDir
	cmd.Stdin = bytes.NewReader(packfile)
	return cmd.Run()
}

// parseReceivePackBody parses the Git Smart HTTP receive-pack request body
// Returns the packfile data after the flush packet
func parseReceivePackBody(body []byte) ([]byte, error) {
	i := 0
	for i < len(body) {
		// Necesitamos al menos 4 bytes para leer la longitud
		if i+4 > len(body) {
			return nil, fmt.Errorf("incomplete pkt-line at position %d", i)
		}

		// Read 4 hex digits for length
		lengthStr := string(body[i : i+4])
		
		// Verificar si ya llegamos al packfile (empieza con "PACK")
		if lengthStr == "PACK" {
			// El resto es el packfile
			return body[i:], nil
		}
		
		i += 4

		length, err := strconv.ParseInt(lengthStr, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid pkt-line length '%s' at position %d: %w", lengthStr, i-4, err)
		}

		if length == 0 {
			// Flush packet encontrado
			// El packfile puede seguir inmediatamente o podría haber más pkt-lines
			// Verificar si hay más datos
			if i >= len(body) {
				return []byte{}, nil // No packfile
			}
			
			// Continuar parseando por si hay más pkt-lines antes del PACK
			// o retornar el resto si empieza con PACK
			remaining := body[i:]
			if len(remaining) >= 4 && string(remaining[:4]) == "PACK" {
				return remaining, nil
			}
			// Continuar el loop para procesar más pkt-lines
			continue
		}

		// La longitud incluye los 4 bytes del prefijo de longitud
		// Ajustar para obtener solo el tamaño del contenido
		dataLen := int(length) - 4
		
		if dataLen < 0 {
			return nil, fmt.Errorf("invalid data length %d at position %d", dataLen, i-4)
		}
		
		if i+dataLen > len(body) {
			return nil, fmt.Errorf("pkt-line data length %d exceeds remaining data at position %d", dataLen, i)
		}
		
		// Saltar el contenido del pkt-line
		i += dataLen
	}

	return nil, fmt.Errorf("no packfile found in receive-pack body")
}
