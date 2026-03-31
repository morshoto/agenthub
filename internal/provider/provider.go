package provider

// Provider is the common boundary for cloud-specific implementations.
//
// Epic 1 only needs the interface location so the CLI/runtime layers can
// depend on a stable abstraction before AWS-specific behavior is added.
type Provider interface {
	Name() string
}
