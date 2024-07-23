# Hosted/Tenant Rancher Guide

This README provides instructions for running and managing Hosted/Tenant Rancher instances through Jenkins, including setup, execution, cleanup, and local testing.

#### Update: Dynamic Amount of Tenant Ranchers

There is a new field in the `config.yml` file, `total_rancher_instances`,
which allows you to specify the number of tenant Ranchers to create. 

The default should be two and this will create you:

- One Hosted Rancher
- One Tenant Rancher

But if you set this to four `total_rancher_instances: 4` you will get:

- One Hosted Rancher
- Three Tenant Ranchers

## Running in Jenkins

### Prerequisites

- **S3 Bucket**: Ensure you have an S3 bucket exclusively for this purpose. It's a one-time setup, reusable for future runs. A Jenkins cleanup job will handle the Terraform state file deletion in this bucket.
- **Configuration File**: Prepare a `config.yml` file as per the Config File Setup section below. This file is necessary for Jenkins job configuration.

### Time Estimates

Expect the Jenkins job to take approximately 15 minutes, attributed mainly to the provisioning and deletion of RDS Aurora MySQL databases.

### Job Execution Guidelines

- **S3 Bucket Limitation**: Each S3 bucket can only be associated with one hosted/tenant job at a time.
- **Terraform State File**: Presence of a Terraform state file in the S3 bucket necessitates running a cleanup job before initiating a new one.
- **Multiple Instances**: For simultaneous hosted/tenant setups, use unique S3 bucket names for each and note them for subsequent cleanup.

### Cleanup Process

Utilize the same `config.yml` for the Hosted/Tenant Cleanup Jenkins Job. This job initializes the state file in the S3 bucket to facilitate the `terraform destroy` command.

## Config File Setup

The `config.yml` file is crucial for both local and Jenkins executions. Ensure it's present at the repository's root, alongside this README. The following template outlines the necessary configuration, where placeholders should be replaced with your specific details. For Jenkins execution, copy and paste the provided YAML into the job setup.

**Repository URL**: The `rancher.repository_url` can be adjusted to use the latest, alpha, or stable versions. Detailed information about choosing a Rancher version is available at the official Rancher documentation.

### AWS RDS Password Requirements

The `aws_rds_password` in the YAML file must comply with AWS's criteria: a minimum of 8 printable ASCII characters, excluding /, ', ", and @ symbols. Non-compliance will result in Terraform provisioning failure.

```yaml
total_rancher_instances: 2
s3:
  bucket: # Your dedicated S3 Bucket name in AWS us-east-2.
  region: us-east-2
aws:
  rsa_private_key: | 
    -----BEGIN RSA PRIVATE KEY-----
    # Your AWS PEM key contents here.
    -----END RSA PRIVATE KEY-----
rancher:
  repository_url: https://releases.rancher.com/server-charts/latest
  bootstrap_password: # Desired bootstrap password for Rancher.
  version: 2.8.1
  image: rancher/rancher
  image_tag: v2.8-head
  psp_enabled: false
  extra_env_name: ""
  extra_env_value: ""
k3s:
  version: v1.27.8+k3s2
tf_vars:
  aws_access_key: # Your AWS Access Key.
  aws_secret_key: # Your AWS Secret Key.
  aws_prefix: # A short prefix for labeling, preferably your initials.
  aws_vpc: # The VPC ID to use.
  aws_subnet_a: # Subnet A ID.
  aws_subnet_b: # Subnet B ID.
  aws_subnet_c: # Subnet C ID.
  aws_ami: # The AMI to use.
  aws_subnet_id: # The Subnet ID to use.
  aws_security_group_id: # The Security Group ID to use.
  aws_pem_key_name: # Your PEM key name in AWS (no file extension).
  aws_rds_password: # AWS RDS password following the specified criteria.
  aws_route53_fqdn: # Your Route53 FQDN.
  aws_ec2_instance_type: m5.xlarge
upgrade:
  path: tenant-1 # e.g., "host", "tenant-1", "tenant-2"
  version: ""
  image: rancher/rancher
  image_tag: v2.8-head
  extra_env_name: ""
  extra_env_value: ""
```

### Local Execution and Upgrade
For local testing and upgrade processes, specific functions in `/terratest/test/host_test.go` facilitate the creation and upgrade of hosted and tenant Ranchers. Expect similar time frames (~15 minutes) due to Terraform and AWS operations, primarily for RDS Aurora MySQL database setups.

- **Creation**: Execute `TestCreateHostedTenantRancher` to initiate a hosted Rancher and an imported tenant Rancher setup.
- **Upgrade**: Run `TestUpgradeRancher` for upgrading existing setups. This looks for the variable in the config file under upgrade, path. This is expecting "host", "tenant-1", "tenant-2", etc., etc.

Upon successful execution, URLs for both host and tenant Ranchers will be provided.
sting and upgrade processes, specific functions in /terratest/test/host_test.go facilitate the creation and upgrade of hosted and tenant Ranchers. Expect similar time frames (~15 minutes) due to Terraform and AWS operations, primarily for RDS Aurora MySQL database setups.