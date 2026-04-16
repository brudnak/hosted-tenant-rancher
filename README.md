# Hosted/Tenant Rancher Guide

This repo creates a hosted Rancher plus one or more tenant Ranchers on imported downstream K3s clusters, with a simpler config flow and optional auto-version resolution.

## Overview

This system creates a **Host Rancher** that manages multiple **Tenant Ranchers** as imported clusters. Each instance can run different versions of K3S and Rancher, allowing you to test version compatibility and upgrade scenarios.

### Architecture

- **Host Rancher (Index 0)**: The primary Rancher instance that manages tenant clusters
- **Tenant Ranchers (Index 1+)**: Secondary Rancher instances running as imported clusters

### Two-Phase Installation Process

1. **Phase 1**: Install K3S on all instances and import tenants as plain clusters
2. **Phase 2**: Install Rancher on each imported tenant cluster using cluster-specific Helm commands

## Prerequisites

- Dedicated S3 bucket for Terraform state storage
- Existing AWS VPC, subnets, AMI, and security group values
- A repo-root `tool-config.yml`
- Local `kubectl`, `helm`, and `terraform`

## Simpler Config

Copy one of these examples to `tool-config.yml`:

- [tool-config.auto.example.yml](/Users/andrewbrudnak/github.com/brudnak/hosted-tenant-rancher/tool-config.auto.example.yml)
- [tool-config.manual.example.yml](/Users/andrewbrudnak/github.com/brudnak/hosted-tenant-rancher/tool-config.manual.example.yml)

`tool-config.yml` replaces the old `config.yml` flow. The test code still falls back to `config.yml` for compatibility, but the new path is `tool-config.yml`.

### Secrets

These values now come from environment variables instead of config:

- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_PASSWORD`

`DOCKERHUB_USERNAME` and `DOCKERHUB_PASSWORD` are optional. If both are unset, the repo skips `registries.yaml` generation and K3s will pull anonymously.

The easiest local setup is `~/.zprofile`:

```bash
export AWS_ACCESS_KEY_ID="your-aws-access-key"
export AWS_SECRET_ACCESS_KEY="your-aws-secret-key"
export DOCKERHUB_USERNAME="your-dockerhub-username"
export DOCKERHUB_PASSWORD="your-dockerhub-password"
```

### Config Shape

- `total_rancher_instances`: total host + tenant instances, `2-4`
- `rancher.mode`: `manual` or `auto`
- `s3.*`: backend bucket/region
- `tf_vars.*`: non-secret AWS/Terraform inputs

Index mapping is still:

    Index 0: Host Rancher + Host K3S
    Index 1: Tenant 1 Rancher + Tenant 1 K3S
    Index 2: Tenant 2 Rancher + Tenant 2 K3S
    Index 3: Tenant 3 Rancher + Tenant 3 K3S

## Auto Mode

Use `rancher.mode: auto` when you want to give Rancher versions and let the tool resolve the rest.

In auto mode the test will:

1. Resolve the right Rancher chart source and chart version.
2. Resolve image overrides for head/alpha/rc builds when needed.
3. Read the SUSE support matrix for the chosen Rancher compatibility baseline.
4. Pick the highest supported K3s minor and latest patch in that line.
5. Download and hash the exact K3s installer and airgap bundle URLs.
6. Generate `rancher.helm_commands` and `k3s.*` values in memory.
7. Print the plan and, on macOS, show a GoLand-friendly native confirmation dialog unless `rancher.auto_approve: true`.

Auto mode accepts:

- `rancher.version` for a single instance
- `rancher.versions` for multiple instances
- `rancher.distro` with `auto`, `community`, or `prime`
- `rancher.bootstrap_password`
- `rancher.auto_approve`

## Manual Mode

Use `rancher.mode: manual` when you want full control over Helm commands and K3s versions.

Manual mode accepts:

- `rancher.helm_commands`
- `k3s.version` or `k3s.versions`
- `k3s.install_script_sha256` or `k3s.install_script_sha256s`
- `k3s.airgap_image_sha256` or `k3s.airgap_image_sha256s`
- `k3s.preload_images`

## Remote Execution

The test runner now uses AWS Systems Manager Run Command instead of SSH.

### What changed
- No local IP whitelist check before Terraform runs
- No SSH private key required for remote commands
- EC2 instances get an SSM instance profile and bootstrap the SSM agent during provisioning

## K3s Registry Setup

The node bootstrap now prepares K3s config files before installation:
- `/etc/rancher/k3s/config.yaml` for the shared datastore and TLS SANs
- `/etc/rancher/k3s/registries.yaml` when Docker Hub credentials are present
- `/var/lib/rancher/k3s/agent/images/` with the K3s image tarball when preloading is enabled

This “preload” path is K3s’s documented airgap image import mechanism. In this repo it is being used as an online optimization to reduce registry pulls and avoid Docker Hub throttling during bootstrap.

The K3s bootstrap path verifies downloaded upstream artifacts before using them:
- The repo does not use `curl | sh` for the K3s installer.
- The installer is downloaded from the exact version tag.
- The installer must match the pinned SHA256 before it runs.
- The airgap image bundle must match the pinned SHA256 before it is moved into the K3s image import directory.

### Updating K3s Checksums

Update the K3s checksums whenever you add or change an entry in `k3s.versions`.

For each K3s version, download the exact installer script from the version tag and compute its SHA256:

```bash
export K3S_VERSION="v1.33.7+k3s3"
curl -fsSL "https://raw.githubusercontent.com/k3s-io/k3s/${K3S_VERSION/+/%2B}/install.sh" -o /tmp/k3s-install.sh
shasum -a 256 /tmp/k3s-install.sh
```

Then download the matching airgap image bundle and compute its SHA256:

```bash
export K3S_VERSION="v1.33.7+k3s3"
curl -fsSL "https://github.com/k3s-io/k3s/releases/download/${K3S_VERSION/+/%2B}/k3s-airgap-images-amd64.tar.zst" -o /tmp/k3s-airgap-images-amd64.tar.zst
shasum -a 256 /tmp/k3s-airgap-images-amd64.tar.zst
```

Copy only the hash on the left into `tool-config.yml`:

```yaml
k3s:
  install_script_sha256s:
    v1.33.7+k3s3: "9ca7930c31179d83bc13de20078fd8ad3e1ee00875b31f39a7e524ca4ef7d9de"
  airgap_image_sha256s:
    v1.33.7+k3s3: "b0d7062008fa7fcad9ad7c6b60f74ae1c561927dbb5a4105433f5afbd091361b"
```

## Usage

### Running the Test

Execute the hosted/tenant setup with 60 minute timeout:

```bash
go test -v -run TestHosted -timeout 60m
```

### Cleanup

Remove all resources when finished with 60 minute timeout:

```bash
go test -v -run TestCleanup -timeout 60m
```

## Installation Workflow

### Phase 1: Infrastructure & Host Setup
1. **Terraform Apply**: Provisions AWS infrastructure (EC2, RDS, Route53)
2. **Host K3S Installation**: Installs K3S on host using version from index 0
3. **Host Rancher Installation**: Installs Rancher on host using Helm command from index 0
4. **Wait for Stability**: Ensures host Rancher is responding (accepts 200/302/401/403/404 status codes)
5. **Bootstrap & Configure**: Creates admin token and configures server-url setting

### Phase 2: Tenant K3S & Import
6. **Tenant K3S Installation**: Installs K3S on each tenant using respective version
7. **Cluster Import**: Imports each tenant as a plain cluster into host Rancher
8. **Wait for Active**: Ensures each imported cluster becomes Active in host Rancher

### Phase 3: Tenant Rancher Installation
9. **Tenant Rancher Installation**: Installs Rancher on each Active tenant cluster
10. **Final Verification**: Confirms all tenant Ranchers are stable and accessible

## Key Features

### Version Matrix Testing
- **Different K3S Versions**: Each instance can run a different K3S version
- **Different Rancher Versions**: Each instance can run a different Rancher version
- **Upgrade Path Testing**: Test compatibility between versions

### Validation & Safety
- **Array Count Validation**: Ensures K3S versions and Helm commands match instance count
- **S3 State Checking**: Prevents conflicts with existing deployments
- **Progressive Installation**: Waits for each phase to complete before proceeding

### Flexible Configuration
- **2-4 Instances Supported**: Minimum 1 host + 1 tenant, maximum 1 host + 3 tenants
- **Custom Helm Commands**: Each Rancher instance can have unique installation parameters
- **Repository Flexibility**: Mix alpha, latest, and stable chart repositories

## Important Notes

### AWS RDS Password Requirements
The `aws_rds_password` must meet AWS criteria:
- Minimum 8 printable ASCII characters
- Cannot contain: /, ', ", @ symbols

### S3 Bucket Usage
- **One deployment per bucket**: Each S3 bucket can only host one active deployment
- **Cleanup required**: Run `TestCleanup` before starting a new deployment in the same bucket

### Hostname Placeholder
The `--set hostname=placeholder` in Helm commands gets automatically replaced with the actual Route53 hostname during installation.

## Troubleshooting

### Common Issues

1. **Validation Errors**: Ensure array counts match `total_rancher_instances`
2. **S3 Conflicts**: Clean up existing deployments before starting new ones
3. **RDS Password**: Verify password meets AWS requirements
4. **Timeout Issues**: The new status code checking should resolve most stability timeout issues

### Monitoring Progress

The system provides detailed logging including:
- Installation progress for each phase
- HTTP status codes during stability checks
- Cluster import and activation status
- Final URLs for all Rancher instances

## Output

Upon successful completion, you'll receive URLs for all instances:

```bash
Host Rancher https://host-rancher.your-domain.com
Tenant Rancher 1 https://tenant1-rancher.your-domain.com  
Tenant Rancher 2 https://tenant2-rancher.your-domain.com
Tenant Rancher 3 https://tenant3-rancher.your-domain.com
```

Each tenant will also appear as an imported cluster in the host Rancher UI.
