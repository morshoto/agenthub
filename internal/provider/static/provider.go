package static

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"agenthub/internal/config"
	"agenthub/internal/provider"
)

type Provider struct {
	Platform     string
	Profile      string
	ComputeClass string
}

func New(platform, profile, computeClass string) *Provider {
	return &Provider{
		Platform:     strings.ToLower(strings.TrimSpace(platform)),
		Profile:      strings.TrimSpace(profile),
		ComputeClass: strings.TrimSpace(computeClass),
	}
}

var _ provider.CloudProvider = (*Provider)(nil)

func (p *Provider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{
		Profile: p.Profile,
		Account: "local",
		Arn:     fmt.Sprintf("arn:agenthub:%s:local", p.platformName()),
		UserID:  p.platformName(),
	}, nil
}

func (p *Provider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return p.CheckAuth(ctx)
}

func (p *Provider) ListRegions(ctx context.Context) ([]string, error) {
	regions := append([]string(nil), staticRegionsForPlatform(p.Platform)...)
	if len(regions) == 0 {
		return nil, fmt.Errorf("no regions configured for platform %q", p.platformName())
	}
	return regions, nil
}

func (p *Provider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return provider.GPUQuotaReport{}, errors.New("region is required")
	}
	return provider.GPUQuotaReport{
		Source:          "mock",
		Region:          region,
		InstanceFamily:  strings.TrimSpace(instanceFamily),
		LikelyCreatable: true,
		Notes: []string{
			fmt.Sprintf("%s quota checks are mocked in the current scaffold", p.platformName()),
		},
	}, nil
}

func (p *Provider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return nil, errors.New("region is required")
	}
	items := staticInstanceTypesForPlatform(p.Platform, computeClass)
	if len(items) == 0 {
		return nil, fmt.Errorf("no instance types configured for platform %q", p.platformName())
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (p *Provider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return p.RecommendInstanceTypes(ctx, region, p.ComputeClass)
}

func (p *Provider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return nil, errors.New("region is required")
	}
	images := staticBaseImagesForPlatform(p.Platform, region, computeClass)
	if len(images) == 0 {
		return nil, fmt.Errorf("no base images configured for platform %q", p.platformName())
	}
	return images, nil
}

func (p *Provider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return p.RecommendBaseImages(ctx, region, p.ComputeClass)
}

func (p *Provider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return nil, fmt.Errorf("instance lookup is not implemented for platform %q", p.platformName())
}

func (p *Provider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	return nil
}

func (p *Provider) platformName() string {
	if strings.TrimSpace(p.Platform) != "" {
		return p.Platform
	}
	return "unknown"
}

func staticRegionsForPlatform(platform string) []string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case config.PlatformAWS:
		return []string{"us-east-1", "us-west-2", "ap-northeast-1"}
	case config.PlatformGCP:
		return []string{"us-central1", "us-east1", "asia-northeast1"}
	case config.PlatformAzure:
		return []string{"japaneast", "eastus", "westeurope"}
	default:
		return []string{"us-east-1"}
	}
}

func staticInstanceTypesForPlatform(platform, computeClass string) []provider.InstanceType {
	class := config.EffectiveComputeClass(computeClass)
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case config.PlatformGCP:
		if class == config.ComputeClassCPU {
			return []provider.InstanceType{
				{Name: "e2-medium", MemoryGB: 4},
				{Name: "e2-standard-4", MemoryGB: 16},
			}
		}
		return []provider.InstanceType{
			{Name: "a2-highgpu-1g", GPUCount: 1, MemoryGB: 85},
		}
	case config.PlatformAzure:
		if class == config.ComputeClassCPU {
			return []provider.InstanceType{
				{Name: "Standard_B2s", MemoryGB: 4},
				{Name: "Standard_D4s_v5", MemoryGB: 16},
			}
		}
		return []provider.InstanceType{
			{Name: "Standard_NC4as_T4_v3", GPUCount: 1, MemoryGB: 28},
		}
	default:
		if class == config.ComputeClassCPU {
			return []provider.InstanceType{
				{Name: "t3.medium", MemoryGB: 4},
				{Name: "t3.xlarge", MemoryGB: 16},
			}
		}
		return []provider.InstanceType{
			{Name: "g5.xlarge", GPUCount: 1, MemoryGB: 16},
			{Name: "g6.xlarge", GPUCount: 1, MemoryGB: 16},
		}
	}
}

func staticBaseImagesForPlatform(platform, region, computeClass string) []provider.BaseImage {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case config.PlatformGCP:
		return []provider.BaseImage{
			{
				Name:         "Ubuntu 22.04 LTS",
				ID:           "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts",
				Region:       region,
				Source:       "static",
				Architecture: "x86_64",
			},
		}
	case config.PlatformAzure:
		return []provider.BaseImage{
			{
				Name:         "Ubuntu 22.04 LTS",
				ID:           "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest",
				Region:       region,
				Source:       "static",
				Architecture: "x86_64",
			},
		}
	default:
		return []provider.BaseImage{
			{
				Name:         "Ubuntu 22.04 LTS",
				ID:           "ami-0123456789abcdef0",
				Region:       region,
				Source:       "static",
				Architecture: "x86_64",
			},
		}
	}
}
