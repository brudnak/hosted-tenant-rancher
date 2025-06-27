# Hosted/Tenant Rancher Guide

This README provides instructions for running and managing multiple Hosted/Tenant Rancher instances with different K3S and Rancher versions.

## Overview

This system creates a **Host Rancher** that manages multiple **Tenant Ranchers** as imported clusters. Each instance can run different versions of K3S and Rancher, allowing you to test version compatibility and upgrade scenarios.

### Architecture

- **Host Rancher (Index 0)**: The primary Rancher instance that manages tenant clusters
- **Tenant Ranchers (Index 1+)**: Secondary Rancher instances running as imported clusters

### Two-Phase Installation Process

1. **Phase 1**: Install K3S on all instances and import tenants as plain clusters
2. **Phase 2**: Install Rancher on each imported tenant cluster using cluster-specific Helm commands

## Prerequisites

- **S3 Bucket**: Dedicated S3 bucket for Terraform state storage
- **AWS Resources**: VPC, subnets, security groups, and PEM key configured
- **Configuration File**: Properly configured `config.yml` file (see below)

## Configuration Setup

The `config.yml` file now uses **array-based configuration** to support multiple instances with different versions. Place this file at the repository root.

### Key Configuration Changes

- **`total_rancher_instances`**: Total number of Rancher instances (2-4 supported)
- **`k3s.versions`**: Array of K3S versions (one per instance)
- **`rancher.helm_commands`**: Array of Helm installation commands (one per instance)

### Array Index Mapping

    Index 0: Host Rancher + Host K3S
    Index 1: Tenant 1 Rancher + Tenant 1 K3S  
    Index 2: Tenant 2 Rancher + Tenant 2 K3S
    Index 3: Tenant 3 Rancher + Tenant 3 K3S

### Example Configuration

```yaml
total_rancher_instances: 4  # 1 host + 3 tenants

s3:
  bucket: your-dedicated-s3-bucket
  region: us-east-2

k3s:
  versions:
    - v1.32.5+k3s1  # Host K3S version
    - v1.32.4+k3s1  # Tenant 1 K3S version  
    - v1.31.8+k3s1  # Tenant 2 K3S version
    - v1.30.11+k3s1 # Tenant 3 K3S version

rancher:
  helm_commands:
    - |
      # Host Rancher (alpha/head version)
      helm repo add rancher-alpha https://releases.rancher.com/server-charts/alpha
      helm repo update
      helm install rancher rancher-alpha/rancher \
        --namespace cattle-system \
        --set hostname=placeholder \
        --set bootstrapPassword=your-password \
        --set global.cattle.psp.enabled=false \
        --version 2.12.0-alpha16 \
        --set rancherImageTag=head \
        --set tls=external \
        --set agentTLSMode=system-store
    - |
      # Tenant 1 Rancher (latest stable)
      helm repo add rancher-latest https://releases.rancher.com/server-charts/latest
      helm repo update
      helm install rancher rancher-latest/rancher \
        --namespace cattle-system \
        --set hostname=placeholder \
        --set bootstrapPassword=your-password \
        --set global.cattle.psp.enabled=false \
        --set tls=external \
        --set agentTLSMode=system-store \
        --version 2.11.3
    - |
      # Tenant 2 Rancher (previous version)
      helm repo add rancher-latest https://releases.rancher.com/server-charts/latest
      helm repo update
      helm install rancher rancher-latest/rancher \
        --namespace cattle-system \
        --set hostname=placeholder \
        --set bootstrapPassword=your-password \
        --set global.cattle.psp.enabled=false \
        --set tls=external \
        --set agentTLSMode=system-store \
        --version 2.11.2
    - |
      # Tenant 3 Rancher (older version)
      helm repo add rancher-latest https://releases.rancher.com/server-charts/latest
      helm repo update
      helm install rancher rancher-latest/rancher \
        --namespace cattle-system \
        --set hostname=placeholder \
        --set bootstrapPassword=your-password \
        --set global.cattle.psp.enabled=false \
        --set tls=external \
        --set agentTLSMode=system-store \
        --version 2.11.1

aws:
  rsa_private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    # Your AWS PEM key contents here
    -----END RSA PRIVATE KEY-----

tf_vars:
  aws_access_key: your-aws-access-key
  aws_secret_key: your-aws-secret-key
  aws_prefix: your-initials
  aws_vpc: vpc-xxxxxxxxx
  aws_subnet_a: subnet-xxxxxxxxx
  aws_subnet_b: subnet-xxxxxxxxx
  aws_subnet_c: subnet-xxxxxxxxx
  aws_ami: ami-xxxxxxxxx
  aws_subnet_id: subnet-xxxxxxxxx
  aws_security_group_id: sg-xxxxxxxxx
  aws_pem_key_name: your-pem-key-name
  aws_rds_password: YourSecurePassword123
  aws_route53_fqdn: your-domain.com
  aws_ec2_instance_type: m5.large
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