package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
)

func (s *SpacesService) protectAgentEffectResult(effectID string, plaintext json.RawMessage) ([]byte, error) {
	if s.aead == nil || effectID == "" {
		return nil, errors.New("effect encryption unavailable")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, []byte("misty-effect-v1:"+effectID)), nil
}

func (s *SpacesService) restoreAgentEffectResult(effectID string, ciphertext []byte) (json.RawMessage, error) {
	if s.aead == nil || len(ciphertext) < s.aead.NonceSize()+s.aead.Overhead() {
		return nil, errors.New("effect replay unavailable")
	}
	size := s.aead.NonceSize()
	return s.aead.Open(nil, ciphertext[:size], ciphertext[size:], []byte("misty-effect-v1:"+effectID))
}
