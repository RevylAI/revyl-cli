package asc

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// tokenLifetime defines how long an ASC JWT token is valid.
// Apple allows up to 20 minutes; we use 15 for safety margin.
const tokenLifetime = 15 * time.Minute

// jwtHeader is the JWT header for App Store Connect API authentication.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// jwtClaims is the JWT claims payload for App Store Connect API authentication.
type jwtClaims struct {
	Iss string `json:"iss"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Aud string `json:"aud"`
}

// GenerateJWT creates a signed JWT for App Store Connect API authentication.
//
// Apple requires ES256-signed JWTs with the following structure:
//   - Header: { alg: "ES256", kid: <keyID>, typ: "JWT" }
//   - Payload: { iss: <issuerID>, iat: <now>, exp: <now+15min>, aud: "appstoreconnect-v1" }
//
// Parameters:
//   - keyID: The App Store Connect API Key ID (10-char alphanumeric)
//   - issuerID: The App Store Connect Issuer ID (UUID format)
//   - privateKey: The loaded ECDSA P-256 private key from the .p8 file
//
// Returns:
//   - string: The signed JWT token string
//   - error: If signing fails
func GenerateJWT(keyID, issuerID string, privateKey *ecdsa.PrivateKey) (string, error) {
	now := time.Now()

	header := jwtHeader{
		Alg: "ES256",
		Kid: keyID,
		Typ: "JWT",
	}

	claims := jwtClaims{
		Iss: issuerID,
		Iat: now.Unix(),
		Exp: now.Add(tokenLifetime).Unix(),
		Aud: "appstoreconnect-v1",
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JWT header: %w", err)
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JWT claims: %w", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	signature, err := signES256([]byte(signingInput), privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + signatureB64, nil
}

// signES256 signs data with ECDSA using the P-256 curve (ES256).
//
// The signature is encoded in the JWS compact format (r || s), not DER.
// Each integer is zero-padded to 32 bytes for P-256.
//
// Parameters:
//   - data: The data to sign (typically the JWT signing input)
//   - key: The ECDSA private key
//
// Returns:
//   - []byte: The 64-byte JWS signature (32 bytes r + 32 bytes s)
//   - error: If signing fails
func signES256(data []byte, key *ecdsa.PrivateKey) ([]byte, error) {
	hash := sha256.Sum256(data)

	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return nil, fmt.Errorf("ECDSA sign failed: %w", err)
	}

	// Convert r and s to fixed-size byte arrays (32 bytes each for P-256)
	curveBits := key.Curve.Params().BitSize
	keyBytes := curveBits / 8
	if curveBits%8 > 0 {
		keyBytes++
	}

	rBytes := r.Bytes()
	sBytes := s.Bytes()

	sig := make([]byte, 2*keyBytes)
	copy(sig[keyBytes-len(rBytes):keyBytes], rBytes)
	copy(sig[2*keyBytes-len(sBytes):], sBytes)

	return sig, nil
}

// GenerateJWTFromPath creates a signed JWT from a .p8 key file path.
//
// Convenience function that loads the key and generates the JWT in one step.
//
// Parameters:
//   - keyID: The App Store Connect API Key ID
//   - issuerID: The App Store Connect Issuer ID
//   - privateKeyPath: Path to the .p8 private key file
//
// Returns:
//   - string: The signed JWT token string
//   - error: If key loading or signing fails
func GenerateJWTFromPath(keyID, issuerID, privateKeyPath string) (string, error) {
	key, err := LoadPrivateKey(privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to load private key: %w", err)
	}
	return GenerateJWT(keyID, issuerID, key)
}
