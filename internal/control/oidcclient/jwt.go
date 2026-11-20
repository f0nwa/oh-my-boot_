package oidcclient

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// JWK represents a single JSON Web Key from an OIDC JWKS endpoint.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	// RSA fields
	N string `json:"n"`
	E string `json:"e"`
	// EC fields
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// JWKS is a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// VerifyJWT verifies a compact JWS against the provided JWKS and returns raw JSON claims.
func VerifyJWT(token string, jwks JWKS) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed JWT")
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode JWT header: %w", err)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return nil, fmt.Errorf("parse JWT header: %w", err)
	}
	if hdr.Alg == "" || strings.EqualFold(hdr.Alg, "none") {
		return nil, errors.New("unsupported JWT alg")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode JWT signature: %w", err)
	}
	signingInput := []byte(parts[0] + "." + parts[1])

	candidates := selectJWKs(jwks, hdr.Kid, hdr.Alg)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no matching JWK found for kid=%q alg=%q", hdr.Kid, hdr.Alg)
	}
	for _, k := range candidates {
		if verifySignature(k, hdr.Alg, signingInput, sigBytes) == nil {
			return payloadBytes, nil
		}
	}
	return nil, errors.New("JWT signature verification failed")
}

func selectJWKs(jwks JWKS, kid, alg string) []JWK {
	var out []JWK
	for _, k := range jwks.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if kid != "" && k.Kid != "" && k.Kid != kid {
			continue
		}
		if k.Alg != "" && k.Alg != alg {
			continue
		}
		out = append(out, k)
	}
	return out
}

func verifySignature(k JWK, alg string, signingInput, sig []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		if k.Kty != "RSA" {
			return errors.New("key type mismatch")
		}
		pub, err := parseRSAPublicKey(k)
		if err != nil {
			return err
		}
		var h crypto.Hash
		var digest []byte
		switch alg {
		case "RS256":
			h = crypto.SHA256
			sum := sha256.Sum256(signingInput)
			digest = sum[:]
		case "RS384":
			h = crypto.SHA384
			sum := sha512.Sum384(signingInput)
			digest = sum[:]
		case "RS512":
			h = crypto.SHA512
			sum := sha512.Sum512(signingInput)
			digest = sum[:]
		}
		return rsa.VerifyPKCS1v15(pub, h, digest, sig)

	case "ES256", "ES384":
		if k.Kty != "EC" {
			return errors.New("key type mismatch")
		}
		pub, keySize, digest, err := parseECPublicKeyAndHash(k, alg, signingInput)
		if err != nil {
			return err
		}
		if len(sig) != 2*keySize {
			return errors.New("invalid ECDSA signature length")
		}
		r := new(big.Int).SetBytes(sig[:keySize])
		s := new(big.Int).SetBytes(sig[keySize:])
		if !ecdsa.Verify(pub, digest, r, s) {
			return errors.New("invalid ECDSA signature")
		}
		return nil
	default:
		return fmt.Errorf("unsupported JWT algorithm %q", alg)
	}
}

func parseRSAPublicKey(k JWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil || len(nBytes) == 0 {
		return nil, errors.New("invalid RSA modulus")
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil || len(eBytes) == 0 {
		return nil, errors.New("invalid RSA exponent")
	}
	e := 0
	for _, b := range eBytes {
		e = (e << 8) | int(b)
	}
	if e < 3 {
		return nil, errors.New("invalid RSA exponent value")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: e,
	}, nil
}

func parseECPublicKeyAndHash(k JWK, alg string, signingInput []byte) (*ecdsa.PublicKey, int, []byte, error) {
	var curve elliptic.Curve
	var keySize int
	var digest []byte
	switch alg {
	case "ES256":
		if k.Crv != "P-256" {
			return nil, 0, nil, errors.New("curve mismatch for ES256")
		}
		curve = elliptic.P256()
		keySize = 32
		sum := sha256.Sum256(signingInput)
		digest = sum[:]
	case "ES384":
		if k.Crv != "P-384" {
			return nil, 0, nil, errors.New("curve mismatch for ES384")
		}
		curve = elliptic.P384()
		keySize = 48
		sum := sha512.Sum384(signingInput)
		digest = sum[:]
	default:
		return nil, 0, nil, fmt.Errorf("unsupported EC alg %q", alg)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, 0, nil, errors.New("invalid EC X")
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, 0, nil, errors.New("invalid EC Y")
	}
	pub := &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}
	return pub, keySize, digest, nil
}
