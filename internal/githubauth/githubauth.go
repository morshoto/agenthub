package githubauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"agenthub/internal/config"
)

type Credential struct {
	Username string
	Password string
}

func InstallationToken(ctx context.Context, region string, cfg config.GitHubConfig) (string, error) {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.InstallationID) == "" || strings.TrimSpace(cfg.PrivateKeySecretARN) == "" {
		return "", errors.New("github auth requires app id, installation id, and private key secret arn")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(strings.TrimSpace(region)))
	if err != nil {
		return "", fmt.Errorf("load aws config for github auth: %w", err)
	}
	client := secretsmanager.NewFromConfig(awsCfg)
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: awsbase.String(strings.TrimSpace(cfg.PrivateKeySecretARN)),
	})
	if err != nil {
		return "", fmt.Errorf("load github app private key secret %q: %w", strings.TrimSpace(cfg.PrivateKeySecretARN), err)
	}

	privateKey, err := parseRSAPrivateKey(strings.TrimSpace(secretString(out.SecretString)))
	if err != nil {
		return "", err
	}

	jwt, err := buildJWT(strings.TrimSpace(cfg.AppID), privateKey, time.Now().UTC())
	if err != nil {
		return "", err
	}

	reqBody := []byte(`{"permissions":{"contents":"read"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", strings.TrimSpace(cfg.InstallationID)), bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create github installation token request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agenthub")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request github installation token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read github installation token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("github installation token request failed: %s", msg)
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse github installation token response: %w", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
		return "", errors.New("github installation token response did not contain a token")
	}
	return strings.TrimSpace(payload.Token), nil
}

func CredentialForGit(ctx context.Context, region string, cfg config.GitHubConfig) (Credential, error) {
	token, err := InstallationToken(ctx, region, cfg)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		Username: "x-access-token",
		Password: token,
	}, nil
}

func parseRSAPrivateKey(secret string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(secret))
	if block == nil {
		return nil, errors.New("github app private key secret is not valid pem")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse github app private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app private key is not an rsa private key")
	}
	return key, nil
}

func buildJWT(appID string, privateKey *rsa.PrivateKey, now time.Time) (string, error) {
	if strings.TrimSpace(appID) == "" {
		return "", errors.New("github app id is required")
	}
	if privateKey == nil {
		return "", errors.New("github app private key is required")
	}

	header, err := json.Marshal(map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	})
	if err != nil {
		return "", fmt.Errorf("marshal jwt header: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal jwt payload: %w", err)
	}

	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload

	hashed := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("sign github app jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func secretString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
