package api

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestDeploymentSignatureAcceptsCurrentSignedBody(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"external_deployment_id":"deploy-42"}`)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := SignDeploymentRequest([]byte("deployment-secret"), timestamp, body)
	if err := VerifyDeploymentSignature([]byte("deployment-secret"), timestamp, signature, body, now); err != nil {
		t.Fatalf("verify valid signature: %v", err)
	}
}

func TestDeploymentSignatureRejectsTamperingAndStaleTimestamps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"external_deployment_id":"deploy-42"}`)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := SignDeploymentRequest([]byte("deployment-secret"), timestamp, body)

	tests := []struct {
		name      string
		secret    []byte
		timestamp string
		signature string
		body      []byte
	}{
		{name: "wrong key", secret: []byte("other-secret"), timestamp: timestamp, signature: signature, body: body},
		{name: "modified body", secret: []byte("deployment-secret"), timestamp: timestamp, signature: signature, body: []byte(`{"external_deployment_id":"deploy-43"}`)},
		{name: "malformed timestamp", secret: []byte("deployment-secret"), timestamp: "tomorrow", signature: signature, body: body},
		{name: "malformed signature", secret: []byte("deployment-secret"), timestamp: timestamp, signature: "sha256=not-hex", body: body},
		{name: "empty key", timestamp: timestamp, signature: signature, body: body},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			if err := VerifyDeploymentSignature(testCase.secret, testCase.timestamp, testCase.signature, testCase.body, now); !errors.Is(err, ErrInvalidDeploymentSignature) {
				t.Fatalf("VerifyDeploymentSignature error = %v, want ErrInvalidDeploymentSignature", err)
			}
		})
	}

	oldTimestamp := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	oldSignature := SignDeploymentRequest([]byte("deployment-secret"), oldTimestamp, body)
	if err := VerifyDeploymentSignature([]byte("deployment-secret"), oldTimestamp, oldSignature, body, now); !errors.Is(err, ErrInvalidDeploymentSignature) {
		t.Fatalf("stale signature error = %v, want ErrInvalidDeploymentSignature", err)
	}
}

func TestDeploymentSignatureRejectsFutureTimestampBeyondSkew(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	body := []byte(`{}`)
	timestamp := strconv.FormatInt(now.Add(6*time.Minute).Unix(), 10)
	signature := SignDeploymentRequest([]byte("deployment-secret"), timestamp, body)
	if err := VerifyDeploymentSignature([]byte("deployment-secret"), timestamp, signature, body, now); !errors.Is(err, ErrInvalidDeploymentSignature) {
		t.Fatalf("future signature error = %v, want ErrInvalidDeploymentSignature", err)
	}
}
