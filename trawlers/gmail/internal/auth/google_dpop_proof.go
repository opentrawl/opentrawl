package auth

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// A DPoP proof (RFC 9449) is sent only to Google's token endpoint. It binds
// the refresh token to the Secure Enclave key, so a copied refresh token is
// useless on another Mac. Gmail API calls carry a plain bearer access token.

type dpopProofHeader struct {
	Type      string                 `json:"typ"`
	Algorithm string                 `json:"alg"`
	PublicKey dpopProofHeaderP256JWK `json:"jwk"`
}

type dpopProofHeaderP256JWK struct {
	KeyType     string `json:"kty"`
	XCoordinate string `json:"x"`
	YCoordinate string `json:"y"`
	Curve       string `json:"crv"`
}

type dpopProofClaims struct {
	ProofIdentifier    string `json:"jti"`
	HTTPMethod         string `json:"htm"`
	HTTPTargetURL      string `json:"htu"`
	IssuedAtUnixSecond int64  `json:"iat"`
	GoogleDPoPNonce    string `json:"nonce,omitempty"`
}

func createGmailDPoPKey() error {
	if err := deleteGmailDPoPKey(); err != nil {
		return err
	}
	return createSecureEnclaveSigningKey(gmailDPoPKeyApplicationTag)
}

func deleteGmailDPoPKey() error {
	err := deleteSecureEnclaveSigningKey(gmailDPoPKeyApplicationTag)
	if errors.Is(err, ErrMacOSKeychainItemNotFound) {
		return nil
	}
	return err
}

func signedDPoPProofForTokenEndpoint(tokenEndpointURL string, googleDPoPNonce string, issuedAt time.Time) (string, error) {
	publicKey, err := copySecureEnclaveSigningKeyPublicKey(gmailDPoPKeyApplicationTag)
	if err != nil {
		return "", err
	}
	jwk, err := p256JWKFromUncompressedPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	proofIdentifier := make([]byte, 32)
	if _, err := rand.Read(proofIdentifier); err != nil {
		return "", err
	}
	header, err := json.Marshal(dpopProofHeader{Type: "dpop+jwt", Algorithm: "ES256", PublicKey: jwk})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(dpopProofClaims{
		ProofIdentifier:    base64.RawURLEncoding.EncodeToString(proofIdentifier),
		HTTPMethod:         "POST",
		HTTPTargetURL:      tokenEndpointURL,
		IssuedAtUnixSecond: issuedAt.Unix(),
		GoogleDPoPNonce:    googleDPoPNonce,
	})
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	derSignature, err := signWithSecureEnclaveSigningKey(gmailDPoPKeyApplicationTag, []byte(signingInput))
	if err != nil {
		return "", err
	}
	joseSignature, err := es256SignatureFromDER(derSignature)
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(joseSignature), nil
}

// p256JWKFromUncompressedPublicKey reads the 65-byte X9.63 form, 04||X||Y,
// that the Secure Enclave exports.
func p256JWKFromUncompressedPublicKey(publicKey []byte) (dpopProofHeaderP256JWK, error) {
	if len(publicKey) != 65 || publicKey[0] != 0x04 {
		return dpopProofHeaderP256JWK{}, errors.New("secure Enclave public key is not an uncompressed P-256 point")
	}
	if _, err := ecdh.P256().NewPublicKey(publicKey); err != nil {
		return dpopProofHeaderP256JWK{}, fmt.Errorf("secure Enclave public key is not on P-256: %w", err)
	}
	return dpopProofHeaderP256JWK{
		KeyType:     "EC",
		XCoordinate: base64.RawURLEncoding.EncodeToString(publicKey[1:33]),
		YCoordinate: base64.RawURLEncoding.EncodeToString(publicKey[33:65]),
		Curve:       "P-256",
	}, nil
}

// es256SignatureFromDER turns the Secure Enclave's DER ECDSA signature into
// the 64-byte R||S form that JWS ES256 requires.
func es256SignatureFromDER(derSignature []byte) ([]byte, error) {
	var signature struct {
		R, S *big.Int
	}
	rest, err := asn1.Unmarshal(derSignature, &signature)
	if err != nil || len(rest) != 0 {
		return nil, errors.New("secure Enclave signature is not DER ECDSA")
	}
	curveOrder := elliptic.P256().Params().N
	for _, value := range []*big.Int{signature.R, signature.S} {
		if value.Sign() <= 0 || value.Cmp(curveOrder) >= 0 {
			return nil, errors.New("secure Enclave signature is out of range for P-256")
		}
	}
	joseSignature := make([]byte, 64)
	signature.R.FillBytes(joseSignature[:32])
	signature.S.FillBytes(joseSignature[32:])
	return joseSignature, nil
}
