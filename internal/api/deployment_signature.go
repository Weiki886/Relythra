package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const deploymentSignatureMaxSkew = 5 * time.Minute

var ErrInvalidDeploymentSignature = errors.New("invalid deployment webhook signature")

func SignDeploymentRequest(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%s.", timestamp)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func DeploymentSignatureReplayKey(signature string) ([]byte, error) {
	if !strings.HasPrefix(signature, "sha256=") {
		return nil, ErrInvalidDeploymentSignature
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrInvalidDeploymentSignature
	}
	digest := sha256.Sum256(decoded)
	return digest[:], nil
}

func DeploymentSignatureExpiresAt(timestamp string) (time.Time, error) {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return time.Time{}, ErrInvalidDeploymentSignature
	}
	return time.Unix(seconds, 0).Add(deploymentSignatureMaxSkew), nil
}

func VerifyDeploymentSignature(
	secret []byte,
	timestamp string,
	signature string,
	body []byte,
	now time.Time,
) error {
	if len(secret) == 0 || len(timestamp) == 0 || !strings.HasPrefix(signature, "sha256=") {
		return ErrInvalidDeploymentSignature
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrInvalidDeploymentSignature
	}
	signedAt := time.Unix(seconds, 0)
	if delta := now.Sub(signedAt); delta > deploymentSignatureMaxSkew || delta < -deploymentSignatureMaxSkew {
		return ErrInvalidDeploymentSignature
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(provided) != sha256.Size {
		return ErrInvalidDeploymentSignature
	}
	expected := strings.TrimPrefix(SignDeploymentRequest(secret, timestamp, body), "sha256=")
	decodedExpected, err := hex.DecodeString(expected)
	if err != nil || !hmac.Equal(provided, decodedExpected) {
		return ErrInvalidDeploymentSignature
	}
	return nil
}
