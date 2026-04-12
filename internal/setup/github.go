package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"

	"agenthub/internal/config"
	"agenthub/internal/githubauth"
)

var gitRemoteOriginURLFunc = defaultGitRemoteOriginURL
var runGitHubAuthLoginFunc = defaultRunGitHubAuthLogin
var runGitHubAuthTokenFunc = defaultRunGitHubAuthToken
var storeGitHubTokenFunc = defaultStoreGitHubToken
var runGitHubAPIUserFunc = defaultRunGitHubAPIUser
var runGitHubAPIUserEmailsFunc = defaultRunGitHubAPIUserEmails
var runGitHubAPIRepoDeployKeyFunc = defaultRunGitHubAPIRepoDeployKey
var storeGitHubSSHKeyFunc = defaultStoreGitHubSSHKey
var ensureGitHubSSHPrivateKeyFunc = defaultEnsureGitHubSSHPrivateKey
var deriveGitHubSSHPublicKeyFunc = defaultDeriveGitHubSSHPublicKey
var bootstrapGitHubSSHCloneFunc = bootstrapGitHubSSHClone
var LookupGitIdentityFunc = lookupGitIdentity

type GitIdentity struct {
	Name  string
	Email string
}

func detectGitHubRepoSlug(ctx context.Context) (string, error) {
	remoteURL, err := gitRemoteOriginURLFunc(ctx)
	if err != nil {
		return "", err
	}
	return parseGitHubRepoSlug(remoteURL), nil
}

func defaultGitRemoteOriginURL(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func parseGitHubRepoSlug(remoteURL string) string {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return ""
	}

	const sshPrefix = "git@github.com:"
	const sshURLPrefix = "ssh://git@github.com/"

	var path string
	switch {
	case strings.HasPrefix(remoteURL, sshPrefix):
		path = strings.TrimPrefix(remoteURL, sshPrefix)
	case strings.HasPrefix(remoteURL, sshURLPrefix):
		path = strings.TrimPrefix(remoteURL, sshURLPrefix)
	default:
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return ""
		}
		if !strings.EqualFold(parsed.Host, "github.com") {
			return ""
		}
		path = parsed.Path
	}

	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return ""
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

func defaultRunGitHubAuthLogin(ctx context.Context) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("gh CLI is required for GitHub user auth")
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "login", "--web", "--git-protocol", "https", "--hostname", "github.com")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run gh auth login: %w", err)
	}
	return nil
}

func defaultRunGitHubAuthToken(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New("gh CLI is required for GitHub user auth")
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run gh auth token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("gh auth token returned an empty token")
	}
	return token, nil
}

func defaultRunGitHubAPIUser(ctx context.Context) (GitIdentity, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return GitIdentity{}, errors.New("gh CLI is required for GitHub identity lookup")
	}
	cmd := exec.CommandContext(ctx, "gh", "api", "user")
	out, err := cmd.Output()
	if err != nil {
		return GitIdentity{}, fmt.Errorf("run gh api user: %w", err)
	}
	var payload struct {
		Name  string `json:"name"`
		Login string `json:"login"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return GitIdentity{}, fmt.Errorf("parse gh api user response: %w", err)
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = strings.TrimSpace(payload.Login)
	}
	return GitIdentity{Name: name}, nil
}

func defaultRunGitHubAPIUserEmails(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New("gh CLI is required for GitHub identity lookup")
	}
	cmd := exec.CommandContext(ctx, "gh", "api", "user/emails")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run gh api user/emails: %w", err)
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(out, &emails); err != nil {
		return "", fmt.Errorf("parse gh api user/emails response: %w", err)
	}
	for _, email := range emails {
		if email.Primary && email.Verified && strings.TrimSpace(email.Email) != "" {
			return strings.TrimSpace(email.Email), nil
		}
	}
	for _, email := range emails {
		if strings.TrimSpace(email.Email) != "" {
			return strings.TrimSpace(email.Email), nil
		}
	}
	return "", errors.New("gh api user/emails did not return an email")
}

func defaultStoreGitHubToken(ctx context.Context, profile, region, secretName, token string) (string, error) {
	return githubauth.StoreToken(ctx, profile, region, secretName, token)
}

func defaultStoreGitHubSSHKey(ctx context.Context, profile, region, secretName, privateKey string) (string, error) {
	secretName = strings.TrimSpace(secretName)
	privateKey = strings.TrimSpace(privateKey)
	if secretName == "" {
		secretName = "agenthub/github-ssh-key"
	}
	if privateKey == "" {
		return "", errors.New("github ssh private key is required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(strings.TrimSpace(region)), awsconfig.WithSharedConfigProfile(strings.TrimSpace(profile)))
	if err != nil {
		return "", fmt.Errorf("load aws config for github ssh secret: %w", err)
	}
	client := secretsmanager.NewFromConfig(awsCfg)

	out, err := client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         awsbase.String(secretName),
		SecretString: awsbase.String(privateKey),
	})
	if err == nil {
		return awsString(out.ARN), nil
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceExistsException" {
		return "", fmt.Errorf("create github ssh secret %q: %w", secretName, err)
	}

	putOut, putErr := client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     awsbase.String(secretName),
		SecretString: awsbase.String(privateKey),
	})
	if putErr != nil {
		return "", fmt.Errorf("update github ssh secret %q: %w", secretName, putErr)
	}
	if arn := awsString(putOut.ARN); arn != "" {
		return arn, nil
	}
	return secretName, nil
}

func defaultEnsureGitHubSSHPrivateKey(ctx context.Context, privateKeyPath string) (string, error) {
	privateKeyPath = strings.TrimSpace(privateKeyPath)
	if privateKeyPath == "" {
		return "", errors.New("github ssh private key path is required")
	}
	if strings.HasPrefix(privateKeyPath, "~") {
		home, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(home) != "" {
			privateKeyPath = filepath.Join(home, strings.TrimPrefix(privateKeyPath, "~/"))
		}
	}
	if _, err := os.Stat(privateKeyPath); err == nil {
		return privateKeyPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read github ssh private key %q: %w", privateKeyPath, err)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return "", errors.New("ssh-keygen is required to create the github deploy key")
	}
	if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0o700); err != nil {
		return "", fmt.Errorf("create github ssh key directory %q: %w", filepath.Dir(privateKeyPath), err)
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", privateKeyPath, "-C", "agenthub-github-deploy-key")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("create github ssh private key %q: %s: %w", privateKeyPath, msg, err)
		}
		return "", fmt.Errorf("create github ssh private key %q: %w", privateKeyPath, err)
	}
	return privateKeyPath, nil
}

func defaultDeriveGitHubSSHPublicKey(ctx context.Context, privateKeyPath string) (string, error) {
	path, err := defaultEnsureGitHubSSHPrivateKey(ctx, privateKeyPath)
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return "", errors.New("ssh-keygen is required to derive the github deploy public key")
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-y", "-f", path)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("derive github ssh public key from %q: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func defaultRunGitHubAPIRepoDeployKey(ctx context.Context, repoSlug, title, publicKey string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("gh CLI is required for github deploy key registration")
	}
	repoSlug = strings.TrimSpace(repoSlug)
	title = strings.TrimSpace(title)
	publicKey = strings.TrimSpace(publicKey)
	if repoSlug == "" || title == "" || publicKey == "" {
		return errors.New("github deploy key registration requires repo slug, title, and public key")
	}
	cmd := exec.CommandContext(ctx, "gh", "api", "repos/"+repoSlug+"/keys", "--method", "POST", "-f", "title="+title, "-f", "key="+publicKey, "-f", "read_only=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("register github deploy key for %s: %s: %w", repoSlug, msg, err)
		}
		return fmt.Errorf("register github deploy key for %s: %w", repoSlug, err)
	}
	return nil
}

func defaultGitHubSSHKeySecretName(repoSlug string) string {
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return "agenthub/github-ssh-key"
	}
	if safe := sanitizeSecretName(repoSlug); safe != "" {
		return "agenthub/github-ssh-key/" + safe
	}
	return "agenthub/github-ssh-key"
}

func defaultGitHubSSHPrivateKeyPath(repoSlug string) string {
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return "~/.ssh/agenthub-github-deploy-key"
	}
	return "~/.ssh/" + sanitizeSecretName(repoSlug) + "-agenthub-deploy-key"
}

func bootstrapGitHubUserAuth(ctx context.Context, profile, region, repoSlug string) (config.GitHubConfig, error) {
	token, err := runGitHubAuthTokenFunc(ctx)
	if err != nil {
		if loginErr := runGitHubAuthLoginFunc(ctx); loginErr != nil {
			return config.GitHubConfig{}, loginErr
		}
		token, err = runGitHubAuthTokenFunc(ctx)
		if err != nil {
			return config.GitHubConfig{}, err
		}
	}

	secretName := defaultGitHubUserSecretName(repoSlug)
	arn, err := storeGitHubTokenFunc(ctx, profile, region, secretName, token)
	if err != nil {
		return config.GitHubConfig{}, err
	}
	return config.GitHubConfig{
		AuthMode:       config.GitHubAuthModeUser,
		TokenSecretARN: arn,
	}, nil
}

func bootstrapGitHubSSHClone(ctx context.Context, profile, region, repoSlug, privateKeyPath string) (config.GitHubConfig, string, error) {
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return config.GitHubConfig{}, "", errors.New("github repo slug is required")
	}
	resolvedKeyPath, err := ensureGitHubSSHPrivateKeyFunc(ctx, privateKeyPath)
	if err != nil {
		return config.GitHubConfig{}, "", err
	}
	publicKey, err := deriveGitHubSSHPublicKeyFunc(ctx, resolvedKeyPath)
	if err != nil {
		return config.GitHubConfig{}, "", err
	}
	title := "agenthub deploy key for " + repoSlug
	if err := runGitHubAPIRepoDeployKeyFunc(ctx, repoSlug, title, publicKey); err != nil {
		return config.GitHubConfig{}, "", err
	}
	secretName := defaultGitHubSSHKeySecretName(repoSlug)
	arn, err := storeGitHubSSHKeyFunc(ctx, profile, region, secretName, mustReadFile(resolvedKeyPath))
	if err != nil {
		return config.GitHubConfig{}, "", err
	}
	return config.GitHubConfig{
		SSHKeySecretARN: arn,
	}, resolvedKeyPath, nil
}

func mustReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func awsString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func lookupGitIdentity(ctx context.Context) (GitIdentity, error) {
	identity := GitIdentity{}
	user, err := runGitHubAPIUserFunc(ctx)
	if err == nil {
		identity.Name = strings.TrimSpace(user.Name)
	}
	email, err := runGitHubAPIUserEmailsFunc(ctx)
	if err == nil {
		identity.Email = strings.TrimSpace(email)
	}
	if strings.TrimSpace(identity.Name) == "" && strings.TrimSpace(identity.Email) == "" {
		if err != nil {
			return GitIdentity{}, err
		}
		return GitIdentity{}, errors.New("github identity lookup returned no usable values")
	}
	return identity, nil
}

func defaultGitHubUserSecretName(repoSlug string) string {
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return "agenthub/github-token"
	}
	if safe := sanitizeSecretName(repoSlug); safe != "" {
		return "agenthub/github-token/" + safe
	}
	return "agenthub/github-token"
}

func sanitizeSecretName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./+=@", r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return strings.Trim(b.String(), "-/")
}
