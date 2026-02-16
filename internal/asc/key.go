// Package asc provides a client for the App Store Connect API.
//
// This package implements JWT-based authentication and REST API calls
// for managing iOS app distribution through App Store Connect.
package asc

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// LoadPrivateKey loads an ECDSA private key from a .p8 file.
//
// App Store Connect API keys are ES256 (ECDSA P-256) keys in PEM format.
// This function supports both PKCS#8 and SEC1 EC private key formats.
//
// Parameters:
//   - path: Absolute path to the .p8 private key file
//
// Returns:
//   - *ecdsa.PrivateKey: The parsed ECDSA private key
//   - error: If the file can't be read or the key is invalid
func LoadPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from %s", path)
	}

	// Try PKCS#8 first (standard format for ASC keys)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not ECDSA (got %T)", key)
		}
		return ecKey, nil
	}

	// Fall back to SEC1 EC private key format
	ecKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key (tried PKCS8 and SEC1): %w", err)
	}

	return ecKey, nil
}
