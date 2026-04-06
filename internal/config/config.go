package config

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PlatformAWS   = "aws"
	PlatformGCP   = "gcp"
	PlatformAzure = "azure"
)
const (
	ComputeClassGPU = "gpu"
	ComputeClassCPU = "cpu"
)

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-\d+$`)

type Config struct {
	Platform PlatformConfig `yaml:"platform"`
	Compute  ComputeConfig  `yaml:"compute,omitempty"`
	Region   RegionConfig   `yaml:"region"`
	Instance InstanceConfig `yaml:"instance"`
	Image    ImageConfig    `yaml:"image"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
	GitHub   GitHubConfig   `yaml:"github,omitempty"`
	Slack    SlackConfig    `yaml:"slack,omitempty"`
	SSH      SSHConfig      `yaml:"ssh,omitempty"`
	Infra    InfraConfig    `yaml:"infra,omitempty"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
}

type PlatformConfig struct {
	Name string `yaml:"name"`
}

type ComputeConfig struct {
	Class string `yaml:"class,omitempty"`
}

type RegionConfig struct {
	Name string `yaml:"name"`
}

type InstanceConfig struct {
	Type        string `yaml:"type"`
	DiskSizeGB  int    `yaml:"disk_size_gb"`
	NetworkMode string `yaml:"network_mode,omitempty"`
}

type ImageConfig struct {
	Name string `yaml:"name"`
	ID   string `yaml:"id,omitempty"`
}

type RuntimeConfig struct {
	Endpoint   string      `yaml:"endpoint"`
	Model      string      `yaml:"model,omitempty"`
	Port       int         `yaml:"port,omitempty"`
	Provider   string      `yaml:"provider,omitempty"`
	PublicCIDR string      `yaml:"public_cidr,omitempty"`
	Codex      CodexConfig `yaml:"codex,omitempty"`
}

type CodexConfig struct {
	SecretID string `yaml:"secret_id,omitempty"`
}

type GitHubConfig struct {
	AuthMode            string `yaml:"auth_mode,omitempty"`
	AppID               string `yaml:"app_id,omitempty"`
	InstallationID      string `yaml:"installation_id,omitempty"`
	PrivateKeySecretARN string `yaml:"private_key_secret_arn,omitempty"`
	TokenSecretARN      string `yaml:"token_secret_arn,omitempty"`
}

type SlackConfig struct {
	RuntimeURL      string   `yaml:"runtime_url,omitempty"`
	BotUserID       string   `yaml:"bot_user_id,omitempty"`
	AllowedChannels []string `yaml:"allowed_channels,omitempty"`
}

type SSHConfig struct {
	KeyName              string `yaml:"key_name,omitempty"`
	PrivateKeyPath       string `yaml:"private_key_path,omitempty"`
	GitHubPrivateKeyPath string `yaml:"github_private_key_path,omitempty"`
	CIDR                 string `yaml:"cidr,omitempty"`
	User                 string `yaml:"user,omitempty"`
}

type InfraConfig struct {
	Backend     string `yaml:"backend,omitempty"`
	ModuleDir   string `yaml:"module_dir,omitempty"`
	AWSProfile  string `yaml:"aws_profile,omitempty"`
	Environment string `yaml:"environment,omitempty"`
	InstanceID  string `yaml:"instance_id,omitempty"`
}

type SandboxConfig struct {
	Enabled         bool     `yaml:"enabled"`
	NetworkMode     string   `yaml:"network_mode"`
	UseNemoClaw     bool     `yaml:"use_nemoclaw"`
	FilesystemAllow []string `yaml:"filesystem_allow,omitempty"`
}

type ValidationError struct {
	Fields []FieldError
}

type FieldError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return ""
	}
	var parts []string
	for _, field := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s: %s", field.Field, field.Message))
	}
	sort.Strings(parts)
	return "config validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Add(field, message string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: message})
}

func (e *ValidationError) OrNil() error {
	if e == nil || len(e.Fields) == 0 {
		return nil
	}
	return e
}

func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return &Config{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return &Config{}, nil
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &cfg, nil
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config validation failed: config is nil")
	}

	var v ValidationError

	if class := strings.TrimSpace(cfg.Compute.Class); class != "" && !IsValidComputeClass(class) {
		v.Add("compute.class", fmt.Sprintf("unsupported compute class %q", class))
	}

	platform := strings.ToLower(strings.TrimSpace(cfg.Platform.Name))
	if platform == "" {
		v.Add("platform.name", "is required")
	} else if !IsSupportedPlatform(platform) {
		v.Add("platform.name", fmt.Sprintf("unsupported platform %q", cfg.Platform.Name))
	}

	if cfg.Region.Name == "" {
		v.Add("region.name", "is required")
	} else if platform == PlatformAWS && !awsRegionPattern.MatchString(strings.TrimSpace(cfg.Region.Name)) {
		v.Add("region.name", "must look like an AWS region name such as ap-northeast-1")
	}

	if cfg.Instance.Type == "" {
		v.Add("instance.type", "is required")
	}
	if cfg.Instance.DiskSizeGB <= 0 {
		v.Add("instance.disk_size_gb", "must be greater than 0")
	}

	if cfg.Image.Name == "" {
		v.Add("image.name", "is required")
	}

	provider := strings.ToLower(strings.TrimSpace(cfg.Runtime.Provider))
	if provider == "aws-bedrock" {
		if strings.TrimSpace(cfg.Runtime.Model) == "" {
			v.Add("runtime.model", "is required for aws-bedrock provider")
		}
	} else if cfg.Runtime.Endpoint == "" {
		v.Add("runtime.endpoint", "is required")
	} else if parsed, err := url.Parse(cfg.Runtime.Endpoint); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		v.Add("runtime.endpoint", "must be a valid URL with scheme and host")
	}
	if provider != "codex" && cfg.Runtime.Model == "" {
		v.Add("runtime.model", "is required")
	}
	if cfg.Runtime.Port < 0 {
		v.Add("runtime.port", "must be greater than or equal to 0")
	}
	if provider := strings.TrimSpace(cfg.Runtime.Provider); provider != "" && !IsValidRuntimeProvider(provider) {
		v.Add("runtime.provider", fmt.Sprintf("unsupported provider %q", provider))
	}
	if gh := cfg.GitHub; hasGitHubConfig(gh) {
		explicitMode := strings.ToLower(strings.TrimSpace(gh.AuthMode))
		if explicitMode != "" && explicitMode != GitHubAuthModeApp && explicitMode != GitHubAuthModeUser {
			v.Add("github.auth_mode", fmt.Sprintf("unsupported GitHub auth mode %q", gh.AuthMode))
			goto validateGitHubConfigDone
		}
		mode := GitHubAuthModeFor(gh)
		switch mode {
		case GitHubAuthModeUser:
			if strings.TrimSpace(gh.TokenSecretARN) == "" {
				v.Add("github.token_secret_arn", "is required when github user auth is configured")
			} else if !strings.HasPrefix(strings.TrimSpace(gh.TokenSecretARN), "arn:") {
				v.Add("github.token_secret_arn", "must be a Secrets Manager ARN")
			}
		case GitHubAuthModeApp:
			if strings.TrimSpace(gh.AppID) == "" {
				v.Add("github.app_id", "is required when github app auth is configured")
			} else if _, err := strconv.ParseInt(strings.TrimSpace(gh.AppID), 10, 64); err != nil {
				v.Add("github.app_id", "must be a numeric GitHub App id")
			}
			if strings.TrimSpace(gh.InstallationID) == "" {
				v.Add("github.installation_id", "is required when github app auth is configured")
			} else if _, err := strconv.ParseInt(strings.TrimSpace(gh.InstallationID), 10, 64); err != nil {
				v.Add("github.installation_id", "must be a numeric GitHub installation id")
			}
			if strings.TrimSpace(gh.PrivateKeySecretARN) == "" {
				v.Add("github.private_key_secret_arn", "is required when github app auth is configured")
			} else if !strings.HasPrefix(strings.TrimSpace(gh.PrivateKeySecretARN), "arn:") {
				v.Add("github.private_key_secret_arn", "must be a Secrets Manager ARN")
			}
		default:
			// no-op: config.HasGitHubAuth may be true when the auth fields are
			// incomplete, and the specific validation errors above will cover them.
		}
	}
validateGitHubConfigDone:
	if publicCIDR := strings.TrimSpace(cfg.Runtime.PublicCIDR); publicCIDR != "" {
		if _, err := parseCIDRLike(publicCIDR); err != nil {
			v.Add("runtime.public_cidr", err.Error())
		}
	}
	if mode := EffectiveNetworkMode(cfg); mode != "" && mode != "public" && mode != "private" {
		v.Add("instance.network_mode", "must be public or private")
	}
	if cfg.Infra.Backend != "" && strings.ToLower(strings.TrimSpace(cfg.Infra.Backend)) != "terraform" {
		v.Add("infra.backend", "must be terraform")
	}
	if class := strings.TrimSpace(cfg.Compute.Class); class != "" && platform == PlatformAWS {
		effective := EffectiveComputeClass(class)
		if effective == ComputeClassCPU {
			if strings.TrimSpace(cfg.Instance.Type) != "" && !strings.HasPrefix(strings.TrimSpace(cfg.Instance.Type), "t3.") {
				v.Add("instance.type", "cpu compute should use a general-purpose instance such as t3.xlarge")
			}
		} else if effective == ComputeClassGPU {
			if strings.TrimSpace(cfg.Instance.Type) != "" && !strings.HasPrefix(strings.TrimSpace(cfg.Instance.Type), "g") {
				v.Add("instance.type", "gpu compute should use a GPU-capable instance such as g5.xlarge")
			}
		}
	}

	return v.OrNil()
}

func LoadAndValidate(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("config save failed: config is nil")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("config save failed: output path is required")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("prepare config output directory: %w", err)
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

func EffectiveComputeClass(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case ComputeClassCPU:
		return ComputeClassCPU
	default:
		return ComputeClassGPU
	}
}

func IsValidComputeClass(class string) bool {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case ComputeClassCPU, ComputeClassGPU:
		return true
	default:
		return false
	}
}

func IsSupportedPlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformAWS, PlatformGCP, PlatformAzure:
		return true
	default:
		return false
	}
}

func EffectiveNetworkMode(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	if mode := strings.TrimSpace(cfg.Instance.NetworkMode); mode != "" {
		return mode
	}
	return strings.TrimSpace(cfg.Sandbox.NetworkMode)
}

func IsValidNetworkMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "public", "private":
		return true
	default:
		return false
	}
}

func IsValidRuntimeProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "aws-bedrock", "gemini", "claude-code":
		return true
	default:
		return false
	}
}

func hasGitHubConfig(cfg GitHubConfig) bool {
	return strings.TrimSpace(cfg.AuthMode) != "" ||
		strings.TrimSpace(cfg.AppID) != "" ||
		strings.TrimSpace(cfg.InstallationID) != "" ||
		strings.TrimSpace(cfg.PrivateKeySecretARN) != "" ||
		strings.TrimSpace(cfg.TokenSecretARN) != ""
}

const (
	GitHubAuthModeApp  = "app"
	GitHubAuthModeUser = "user"
)

func GitHubAuthModeFor(cfg GitHubConfig) string {
	mode := strings.ToLower(strings.TrimSpace(cfg.AuthMode))
	switch mode {
	case GitHubAuthModeApp, GitHubAuthModeUser:
		return mode
	case "":
		if strings.TrimSpace(cfg.TokenSecretARN) != "" && strings.TrimSpace(cfg.AppID) == "" && strings.TrimSpace(cfg.InstallationID) == "" && strings.TrimSpace(cfg.PrivateKeySecretARN) == "" {
			return GitHubAuthModeUser
		}
		if strings.TrimSpace(cfg.AppID) != "" || strings.TrimSpace(cfg.InstallationID) != "" || strings.TrimSpace(cfg.PrivateKeySecretARN) != "" {
			return GitHubAuthModeApp
		}
		if strings.TrimSpace(cfg.TokenSecretARN) != "" {
			return GitHubAuthModeUser
		}
		return ""
	default:
		return mode
	}
}

func HasGitHubAuth(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return hasGitHubConfig(cfg.GitHub)
}

func EffectiveTerraformBackend(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	if backend := strings.TrimSpace(cfg.Infra.Backend); backend != "" {
		return strings.ToLower(backend)
	}
	return "terraform"
}

func parseCIDRLike(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("cidr is required")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", fmt.Errorf("invalid cidr %q: %w", value, err)
		}
		return prefix.String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", fmt.Errorf("invalid cidr %q: %w", value, err)
	}
	if addr.Is4() {
		return addr.String() + "/32", nil
	}
	return addr.String() + "/128", nil
}
