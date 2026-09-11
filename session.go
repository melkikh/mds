package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

const maxBrowserSessions = 16

type sessionHash [sha256.Size]byte

func serviceSessionsFile() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "sessions")
}

func hashSession(session string) sessionHash {
	return sha256.Sum256([]byte(session))
}

func readSessionHashes(file string) []sessionHash {
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	hashes := []sessionHash{}
	for _, field := range strings.Fields(string(data)) {
		decoded, err := hex.DecodeString(field)
		if err != nil || len(decoded) != sha256.Size {
			continue
		}
		var hash sessionHash
		copy(hash[:], decoded)
		hashes = append(hashes, hash)
	}
	if len(hashes) > maxBrowserSessions {
		hashes = hashes[len(hashes)-maxBrowserSessions:]
	}
	return hashes
}

func writeSessionHashes(file string, hashes []sessionHash) error {
	if file == "" {
		return nil
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	var body strings.Builder
	for _, hash := range hashes {
		body.WriteString(hex.EncodeToString(hash[:]))
		body.WriteByte('\n')
	}
	if err := os.WriteFile(file, []byte(body.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(file, 0o600)
}

func dropServiceSessions() error {
	file := serviceSessionsFile()
	if file == "" {
		return nil
	}
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *server) acceptsSession(session string) bool {
	want := hashSession(session)
	s.sessionMu.RLock()
	defer s.sessionMu.RUnlock()
	accepted := 0
	for _, known := range s.sessions {
		accepted |= subtle.ConstantTimeCompare(want[:], known[:])
	}
	return accepted == 1
}

func (s *server) issueSession() string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.session != "" {
		return s.session
	}
	s.session = rand.Text()
	s.sessions = append(s.sessions, hashSession(s.session))
	if len(s.sessions) > maxBrowserSessions {
		s.sessions = s.sessions[len(s.sessions)-maxBrowserSessions:]
	}
	// Losing this write only means asking for the key again after the next restart. The
	// plaintext session remains in the browser and this process; only its verifier goes here.
	_ = writeSessionHashes(s.sessionFile, s.sessions)
	return s.session
}
