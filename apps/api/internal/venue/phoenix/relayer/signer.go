// Package relayer holds the backend-only Phoenix fee-payer key. Order messages
// come from the trusted local helper, never from browser-supplied transactions.
package relayer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/mr-tron/base58"
)

const (
	transactionPrefixSize = 1 + 2*ed25519.SignatureSize
	maxMessageSize        = 1232 - transactionPrefixSize
)

var (
	ErrInvalidKey       = errors.New("invalid Phoenix relayer private key: expected base58 Solana 64-byte keypair")
	ErrInvalidMessage   = errors.New("invalid Phoenix helper message")
	ErrSignerMismatch   = errors.New("Phoenix message signer set does not match relayer and expected agent")
	ErrInvalidSignature = errors.New("invalid Phoenix agent signature")
)

type Signer struct {
	key     ed25519.PrivateKey
	address string
}

// NewSigner parses PHOENIX_RELAYER_PRIVATE_KEY supplied by backend configuration.
// The sole accepted format is a base58-encoded 64-byte Solana keypair (seed ||
// public key). Errors never include the secret or decoded bytes.
func NewSigner(secret string) (*Signer, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) > 88 {
		return nil, ErrInvalidKey
	}
	decoded, err := base58.Decode(secret)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, ErrInvalidKey
	}
	key := ed25519.NewKeyFromSeed(decoded[:ed25519.SeedSize])
	if !bytes.Equal(key, decoded) {
		return nil, ErrInvalidKey
	}
	return &Signer{key: key, address: base58.Encode(key[ed25519.SeedSize:])}, nil
}

func (s *Signer) Address() string { return s.address }

// CoSign verifies the expected browser agent's signature and assembles a legacy
// transaction without altering the stored message. built must be the original
// server-stored helper result; expectedAgent must come from the bound signing
// request, not the browser. Neither a digest nor the manifest authorizes intent.
// The live caller still owns one-shot/intent/expiry/fee checks and submission.
func (s *Signer) CoSign(built helper.BuildOrderResult, expectedAgent, signature string) ([]byte, error) {
	if len(built.MessageBase64) > base64.StdEncoding.EncodedLen(maxMessageSize) {
		return nil, ErrInvalidMessage
	}
	message, err := base64.StdEncoding.DecodeString(built.MessageBase64)
	if err != nil || len(message) > maxMessageSize {
		return nil, ErrInvalidMessage
	}
	digest := sha256.Sum256(message)
	expectedDigest, err := hex.DecodeString(built.MessageSHA256)
	if err != nil || !bytes.Equal(expectedDigest, digest[:]) {
		return nil, ErrInvalidMessage
	}
	keys, blockhash, err := messageEnvelope(message)
	if err != nil {
		return nil, err
	}
	if len(expectedAgent) > 44 {
		return nil, ErrSignerMismatch
	}
	agent, err := base58.Decode(expectedAgent)
	if err != nil || len(agent) != ed25519.PublicKeySize ||
		!bytes.Equal(keys[:32], s.key[ed25519.SeedSize:]) ||
		!bytes.Equal(keys[32:64], agent) || bytes.Equal(keys[:32], agent) ||
		len(built.RequiredSigners) != 2 ||
		built.RequiredSigners[0] != (helper.RequiredSigner{Index: 0, Pubkey: s.address, Role: "fee_payer"}) ||
		built.RequiredSigners[1] != (helper.RequiredSigner{Index: 1, Pubkey: expectedAgent, Role: "position_authority"}) {
		return nil, ErrSignerMismatch
	}
	if len(built.RecentBlockhash) > 44 {
		return nil, ErrInvalidMessage
	}
	expectedHash, err := base58.Decode(built.RecentBlockhash)
	if err != nil || len(expectedHash) != 32 || !bytes.Equal(blockhash, expectedHash) {
		return nil, ErrInvalidMessage
	}
	if len(signature) > 88 {
		return nil, ErrInvalidSignature
	}
	agentSignature, err := base58.Decode(signature)
	if err != nil || len(agentSignature) != ed25519.SignatureSize || !ed25519.Verify(agent, message, agentSignature) {
		return nil, ErrInvalidSignature
	}
	transaction := make([]byte, transactionPrefixSize+len(message))
	transaction[0] = 2 // canonical shortvec signature count
	copy(transaction[1:65], ed25519.Sign(s.key, message))
	copy(transaction[65:129], agentSignature)
	copy(transaction[transactionPrefixSize:], message)
	return transaction, nil
}

// Read only the legacy header, account-key vector and blockhash needed to bind
// signatures. Phoenix/Flight instruction semantics stay in the pinned helper.
func messageEnvelope(message []byte) (keys, blockhash []byte, err error) {
	if len(message) < 4 || message[0] != 2 || message[1] > 1 {
		return nil, nil, ErrInvalidMessage
	}
	// The packet size cap means the account count fits one canonical shortvec
	// byte. Two signers and at least one program account are needed.
	count, offset := int(message[3]), 4
	end := offset + count*32
	if count < 3 || count >= 128 || int(message[2]) > count-2 || len(message) <= end+32 {
		return nil, nil, ErrInvalidMessage
	}
	return message[offset:end], message[end : end+32], nil
}
