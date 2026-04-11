package app

import (
	"bytes"
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

type configUpdateChange struct {
	Path   string
	Before string
	After  string
}

func newConfigUpdateCommand(app *App) *cobra.Command {
	var setValues []string
	var agentsDir string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update an existing configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(setValues) == 0 {
				return errors.New("at least one --set key=value update is required")
			}

			configPath := strings.TrimSpace(app.opts.ConfigPath)
			if configPath == "" {
				session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
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

			changes, err := applyConfigUpdates(cfg, setValues)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}
			if len(changes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "configuration is unchanged")
				return nil
			}
			if dryRun {
				printConfigUpdateChanges(cmd.OutOrStdout(), changes)
				fmt.Fprintf(cmd.OutOrStdout(), "dry-run: configuration not written: %s\n", configPath)
				return nil
			}
			if err := config.Save(configPath, cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "configuration updated: %s\n", configPath)
			printConfigUpdateChanges(cmd.OutOrStdout(), changes)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&setValues, "set", nil, "update a config field with key=value; value is parsed as YAML")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview config changes without writing the file")
	return cmd
}

func applyConfigUpdates(cfg *config.Config, rawAssignments []string) ([]configUpdateChange, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	changes := make([]configUpdateChange, 0, len(rawAssignments))
	for _, rawAssignment := range rawAssignments {
		path, rawValue, err := splitConfigUpdateAssignment(rawAssignment)
		if err != nil {
			return nil, err
		}

		field, err := resolveConfigField(reflect.ValueOf(cfg), path)
		if err != nil {
			return nil, err
		}

		beforeValue := reflect.New(field.Type()).Elem()
		beforeValue.Set(field)

		if err := decodeConfigUpdateValue(rawValue, field); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		before := formatConfigUpdateValue(beforeValue.Interface())
		after := formatConfigUpdateValue(field.Interface())
		if before == after {
			continue
		}
		changes = append(changes, configUpdateChange{
			Path:   path,
			Before: before,
			After:  after,
		})
	}

	slices.SortFunc(changes, func(a, b configUpdateChange) int {
		return strings.Compare(a.Path, b.Path)
	})
	return changes, nil
}

func splitConfigUpdateAssignment(raw string) (string, string, error) {
	key, value, ok := strings.Cut(strings.TrimSpace(raw), "=")
	if !ok {
		return "", "", fmt.Errorf("invalid update %q: expected key=value", raw)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("invalid update %q: key is required", raw)
	}
	return key, strings.TrimSpace(value), nil
}

func resolveConfigField(root reflect.Value, path string) (reflect.Value, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return reflect.Value{}, errors.New("field path is required")
	}
	parts := strings.Split(path, ".")
	current := root
	for _, part := range parts {
		for current.Kind() == reflect.Pointer {
			if current.IsNil() {
				current.Set(reflect.New(current.Type().Elem()))
			}
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("unknown config field %q", path)
		}
		next, ok := configFieldByYAMLName(current, part)
		if !ok {
			return reflect.Value{}, fmt.Errorf("unknown config field %q", path)
		}
		current = next
	}

	for current.Kind() == reflect.Pointer {
		if current.IsNil() {
			current.Set(reflect.New(current.Type().Elem()))
		}
		current = current.Elem()
	}
	if !current.CanSet() {
		return reflect.Value{}, fmt.Errorf("config field %q cannot be updated", path)
	}
	return current, nil
}

func configFieldByYAMLName(v reflect.Value, name string) (reflect.Value, bool) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		if tag == "" {
			tag = strings.ToLower(field.Name)
		}
		if tag == "-" || tag != name {
			continue
		}
		return v.Field(i), true
	}
	return reflect.Value{}, false
}

func decodeConfigUpdateValue(raw string, dst reflect.Value) error {
	target := reflect.New(dst.Type())
	if err := yaml.Unmarshal([]byte(raw), target.Interface()); err != nil {
		return fmt.Errorf("decode value: %w", err)
	}
	dst.Set(target.Elem())
	return nil
}

func formatConfigUpdateValue(value any) string {
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return strings.TrimSpace(string(bytes.TrimSpace(data)))
}

func printConfigUpdateChanges(out io.Writer, changes []configUpdateChange) {
	fmt.Fprintln(out, "changes:")
	for _, change := range changes {
		fmt.Fprintf(out, "- %s: %s -> %s\n", change.Path, change.Before, change.After)
	}
}
