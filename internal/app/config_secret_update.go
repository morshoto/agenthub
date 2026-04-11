package app

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"agenthub/internal/config"
	"agenthub/internal/prompt"
)

type secretUpdateTarget string

const (
	secretUpdateTargetConfig secretUpdateTarget = "config"
	secretUpdateTargetEnv    secretUpdateTarget = "env"
)

type secretUpdateSpec struct {
	Path         string
	Target       secretUpdateTarget
	ConfigPath   string
	EnvKey       string
	Secret       bool
	PromptLabel  string
	PromptSecret bool
}

type secretUpdateChange struct {
	Path   string
	Before string
	After  string
}

var secretUpdateSpecs = map[string]secretUpdateSpec{
	"slack.bot_token": {
		Path:         "slack.bot_token",
		Target:       secretUpdateTargetEnv,
		EnvKey:       "SLACK_BOT_TOKEN",
		Secret:       true,
		PromptLabel:  "Slack bot token",
		PromptSecret: true,
	},
	"slack.app_token": {
		Path:         "slack.app_token",
		Target:       secretUpdateTargetEnv,
		EnvKey:       "SLACK_APP_TOKEN",
		Secret:       true,
		PromptLabel:  "Slack app token",
		PromptSecret: true,
	},
	"slack.bot_user_id": {
		Path:        "slack.bot_user_id",
		Target:      secretUpdateTargetEnv,
		EnvKey:      "SLACK_BOT_USER_ID",
		PromptLabel: "Slack bot user ID",
	},
	"slack.allowed_channels": {
		Path:        "slack.allowed_channels",
		Target:      secretUpdateTargetEnv,
		EnvKey:      "SLACK_ALLOWED_CHANNELS",
		PromptLabel: "Slack allowed channels (comma-separated)",
	},
	"codex.api_key": {
		Path:         "codex.api_key",
		Target:       secretUpdateTargetEnv,
		EnvKey:       "OPENAI_API_KEY",
		Secret:       true,
		PromptLabel:  "Codex API key",
		PromptSecret: true,
	},
	"github.auth_mode": {
		Path:        "github.auth_mode",
		Target:      secretUpdateTargetConfig,
		ConfigPath:  "github.auth_mode",
		PromptLabel: "GitHub auth mode",
	},
	"github.app_id": {
		Path:        "github.app_id",
		Target:      secretUpdateTargetConfig,
		ConfigPath:  "github.app_id",
		PromptLabel: "GitHub App ID",
	},
	"github.installation_id": {
		Path:        "github.installation_id",
		Target:      secretUpdateTargetConfig,
		ConfigPath:  "github.installation_id",
		PromptLabel: "GitHub installation ID",
	},
	"github.private_key_secret_arn": {
		Path:        "github.private_key_secret_arn",
		Target:      secretUpdateTargetConfig,
		ConfigPath:  "github.private_key_secret_arn",
		PromptLabel: "GitHub App private key secret ARN",
	},
	"github.token_secret_arn": {
		Path:        "github.token_secret_arn",
		Target:      secretUpdateTargetConfig,
		ConfigPath:  "github.token_secret_arn",
		PromptLabel: "GitHub token secret ARN",
	},
}

func newConfigSecretCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage integration credentials for one agent",
	}
	cmd.AddCommand(newConfigSecretUpdateCommand(app))
	return cmd
}

func newConfigSecretUpdateCommand(app *App) *cobra.Command {
	var setValues []string
	var agentsDir string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update integration credentials for one agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(setValues) == 0 {
				return errors.New("at least one --set key=value update is required")
			}

			session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
			session.Interactive = detectInteractiveInput(cmd.InOrStdin())

			configPath := strings.TrimSpace(app.opts.ConfigPath)
			if configPath == "" {
				selectedConfigPath, err := selectAgentConfigPath(session, agentsDir)
				if err != nil {
					return err
				}
				configPath = selectedConfigPath
				app.opts.ConfigPath = configPath
			}

			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			envPath := agentEnvPathFromConfigPath(configPath)
			envValues, err := loadAgentEnvFile(envPath)
			if err != nil {
				return err
			}
			if envValues == nil {
				envValues = map[string]string{}
			}

			changes, err := applySecretUpdates(cfg, envValues, setValues, session)
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "integration credentials are unchanged")
				return nil
			}
			if dryRun {
				printSecretUpdateChanges(cmd.OutOrStdout(), changes)
				fmt.Fprintf(cmd.OutOrStdout(), "dry-run: integration credentials not written: %s\n", configPath)
				return nil
			}
			if err := writeSecretUpdateFiles(configPath, cfg, envPath, envValues, changes); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "integration credentials updated: %s\n", configPath)
			printSecretUpdateChanges(cmd.OutOrStdout(), changes)
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&setValues, "set", nil, "update an integration credential with key=value")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview secret changes without writing files")
	return cmd
}

func applySecretUpdates(cfg *config.Config, envValues map[string]string, rawAssignments []string, session *prompt.Session) ([]secretUpdateChange, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if envValues == nil {
		return nil, errors.New("env values are required")
	}

	touched := map[string]bool{}
	changes := make([]secretUpdateChange, 0, len(rawAssignments))
	for _, rawAssignment := range rawAssignments {
		path, rawValue, err := splitConfigUpdateAssignment(rawAssignment)
		if err != nil {
			return nil, err
		}
		spec, ok := secretUpdateSpecs[path]
		if !ok {
			return nil, fmt.Errorf("unknown secret field %q", path)
		}
		change, changed, err := applySecretUpdateValue(cfg, envValues, spec, rawValue)
		if err != nil {
			return nil, err
		}
		touched[path] = true
		if changed {
			changes = append(changes, change)
		}
	}

	promptedChanges, err := promptForMissingSecretValues(cfg, envValues, touched, session)
	if err != nil {
		return nil, err
	}
	changes = append(changes, promptedChanges...)

	if err := config.Validate(cfg); err != nil {
		return nil, err
	}

	slices.SortFunc(changes, func(a, b secretUpdateChange) int {
		return strings.Compare(a.Path, b.Path)
	})
	return changes, nil
}

func applySecretUpdateValue(cfg *config.Config, envValues map[string]string, spec secretUpdateSpec, rawValue string) (secretUpdateChange, bool, error) {
	before, err := secretUpdateCurrentValue(cfg, envValues, spec)
	if err != nil {
		return secretUpdateChange{}, false, err
	}

	switch spec.Target {
	case secretUpdateTargetConfig:
		field, err := resolveConfigField(reflect.ValueOf(cfg), spec.ConfigPath)
		if err != nil {
			return secretUpdateChange{}, false, err
		}
		if err := decodeConfigUpdateValue(rawValue, field); err != nil {
			return secretUpdateChange{}, false, fmt.Errorf("%s: %w", spec.Path, err)
		}
	case secretUpdateTargetEnv:
		encoded, err := secretUpdateEncodeEnvValue(spec, rawValue)
		if err != nil {
			return secretUpdateChange{}, false, err
		}
		envValues[spec.EnvKey] = encoded
	default:
		return secretUpdateChange{}, false, fmt.Errorf("unsupported secret update target %q", spec.Target)
	}

	after, err := secretUpdateCurrentValue(cfg, envValues, spec)
	if err != nil {
		return secretUpdateChange{}, false, err
	}
	if before == after {
		return secretUpdateChange{}, false, nil
	}
	return secretUpdateChange{
		Path:   spec.Path,
		Before: secretUpdateDisplayValue(spec, before),
		After:  secretUpdateDisplayValue(spec, after),
	}, true, nil
}

func secretUpdateCurrentValue(cfg *config.Config, envValues map[string]string, spec secretUpdateSpec) (any, error) {
	switch spec.Target {
	case secretUpdateTargetConfig:
		field, err := resolveConfigField(reflect.ValueOf(cfg), spec.ConfigPath)
		if err != nil {
			return nil, err
		}
		return field.Interface(), nil
	case secretUpdateTargetEnv:
		raw := strings.TrimSpace(envValues[spec.EnvKey])
		if spec.Path == "slack.allowed_channels" {
			return splitCommaList(raw), nil
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported secret update target %q", spec.Target)
	}
}

func secretUpdateEncodeEnvValue(spec secretUpdateSpec, rawValue string) (string, error) {
	switch spec.Path {
	case "slack.allowed_channels":
		var channels []string
		if err := yaml.Unmarshal([]byte(rawValue), &channels); err != nil {
			return "", fmt.Errorf("%s: decode value: %w", spec.Path, err)
		}
		return strings.Join(firstNonEmptyStrings(channels), ","), nil
	default:
		return strings.TrimSpace(rawValue), nil
	}
}

func promptForMissingSecretValues(cfg *config.Config, envValues map[string]string, touched map[string]bool, session *prompt.Session) ([]secretUpdateChange, error) {
	required := requiredSecretUpdateSpecs(cfg, touched)
	changes := make([]secretUpdateChange, 0, len(required))
	for _, spec := range required {
		currentValue, err := secretUpdateCurrentValue(cfg, envValues, spec)
		if err != nil {
			return nil, err
		}
		if !secretUpdateValueIsEmpty(currentValue) {
			continue
		}
		if session == nil || !session.Interactive {
			return nil, fmt.Errorf("%s is required", spec.Path)
		}

		var input string
		if spec.PromptSecret {
			input, err = session.Secret(spec.PromptLabel, "")
		} else {
			input, err = session.Text(spec.PromptLabel, "")
		}
		if err != nil {
			return nil, err
		}
		change, changed, err := applySecretUpdateValue(cfg, envValues, spec, input)
		if err != nil {
			return nil, err
		}
		if secretUpdateValueIsEmptyInput(spec, input) {
			return nil, fmt.Errorf("%s is required", spec.Path)
		}
		if changed {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

func requiredSecretUpdateSpecs(cfg *config.Config, touched map[string]bool) []secretUpdateSpec {
	required := map[string]secretUpdateSpec{}

	if touched["slack.bot_token"] || touched["slack.app_token"] {
		required["slack.bot_token"] = secretUpdateSpecs["slack.bot_token"]
		required["slack.app_token"] = secretUpdateSpecs["slack.app_token"]
	}

	touchesGitHub := false
	for path := range touched {
		if strings.HasPrefix(path, "github.") {
			touchesGitHub = true
			break
		}
	}
	if touchesGitHub && cfg != nil {
		mode := config.GitHubAuthModeFor(cfg.GitHub)
		switch mode {
		case config.GitHubAuthModeApp:
			required["github.app_id"] = secretUpdateSpecs["github.app_id"]
			required["github.installation_id"] = secretUpdateSpecs["github.installation_id"]
			required["github.private_key_secret_arn"] = secretUpdateSpecs["github.private_key_secret_arn"]
		case config.GitHubAuthModeUser:
			required["github.token_secret_arn"] = secretUpdateSpecs["github.token_secret_arn"]
		}
		if strings.TrimSpace(cfg.GitHub.AuthMode) != "" {
			required["github.auth_mode"] = secretUpdateSpecs["github.auth_mode"]
		}
	}

	specs := make([]secretUpdateSpec, 0, len(required))
	for _, spec := range required {
		specs = append(specs, spec)
	}
	slices.SortFunc(specs, func(a, b secretUpdateSpec) int {
		return strings.Compare(a.Path, b.Path)
	})
	return specs
}

func secretUpdateValueIsEmpty(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(firstNonEmptyStrings(typed)) == 0
	default:
		return strings.TrimSpace(formatConfigUpdateValue(value)) == ""
	}
}

func secretUpdateValueIsEmptyInput(spec secretUpdateSpec, value string) bool {
	switch spec.Path {
	case "slack.allowed_channels":
		return len(splitCommaList(value)) == 0 && strings.TrimSpace(value) == ""
	default:
		return strings.TrimSpace(value) == ""
	}
}

func secretUpdateDisplayValue(spec secretUpdateSpec, value any) string {
	if spec.Secret {
		if secretUpdateValueIsEmpty(value) {
			return "<empty>"
		}
		return "<redacted>"
	}
	if spec.Path == "slack.allowed_channels" {
		return formatConfigUpdateValue(firstNonEmptyStrings(value.([]string)))
	}
	return formatConfigUpdateValue(value)
}

func writeSecretUpdateFiles(configPath string, cfg *config.Config, envPath string, envValues map[string]string, changes []secretUpdateChange) error {
	writeConfigFile := false
	writeEnvFile := false
	for _, change := range changes {
		spec := secretUpdateSpecs[change.Path]
		if spec.Target == secretUpdateTargetConfig {
			writeConfigFile = true
		}
		if spec.Target == secretUpdateTargetEnv {
			writeEnvFile = true
		}
	}

	if writeConfigFile {
		if err := config.Save(configPath, cfg); err != nil {
			return err
		}
	}
	if writeEnvFile {
		values := make(map[string]string)
		for _, spec := range secretUpdateSpecs {
			if spec.Target != secretUpdateTargetEnv {
				continue
			}
			if value, ok := envValues[spec.EnvKey]; ok {
				values[spec.EnvKey] = value
			}
		}
		if err := appendEnvFile(envPath, values); err != nil {
			return fmt.Errorf("write env file %q: %w", envPath, err)
		}
	}
	return nil
}

func printSecretUpdateChanges(out io.Writer, changes []secretUpdateChange) {
	fmt.Fprintln(out, "changes:")
	for _, change := range changes {
		fmt.Fprintf(out, "- %s: %s -> %s\n", change.Path, change.Before, change.After)
	}
}
