package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

const (
	localRPCAuthProtocolVersion = "lrpc-auth-v1"
	localRPCProofHexLen         = 64
)

func validateHexToken(token string, minLen int, fieldName string) error {
	if len(token) < minLen {
		return fmt.Errorf("%s is too short, need at least %d characters", fieldName, minLen)
	}
	if len(token)%2 != 0 {
		return fmt.Errorf("%s must be an even-length hex string", fieldName)
	}
	if _, err := hex.DecodeString(token); err != nil {
		return fmt.Errorf("%s is not a valid hex string: %w", fieldName, err)
	}
	return nil
}

func generateRandomHex(byteLen int) (string, error) {
	randomBytes := make([]byte, byteLen)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(randomBytes), nil
}

func computeHMACProof(sessionSecret string, clientNonce string, agentNonce string, protocolVersion string, role string) (string, error) {
	if sessionSecret == "" {
		return "", fmt.Errorf("session secret is empty")
	}
	hash := hmac.New(sha256.New, []byte(sessionSecret))
	_, _ = hash.Write([]byte(clientNonce))
	_, _ = hash.Write([]byte(agentNonce))
	_, _ = hash.Write([]byte(protocolVersion))
	_, _ = hash.Write([]byte(role))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func computeAgentProof(sessionSecret string, clientNonce string, agentNonce string, protocolVersion string) (string, error) {
	return computeHMACProof(sessionSecret, clientNonce, agentNonce, protocolVersion, "agent")
}

func computeHostProof(sessionSecret string, clientNonce string, agentNonce string, protocolVersion string) (string, error) {
	return computeHMACProof(sessionSecret, clientNonce, agentNonce, protocolVersion, "host")
}

func constantTimeEqual(left string, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
