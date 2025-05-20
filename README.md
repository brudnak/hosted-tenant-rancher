# Hosted/Tenant Rancher Guide

This README provides instructions for running and managing a Hosted/Tenant Rancher instances.

#### Update: Dynamic Amount of Tenant Ranchers

There is a new field in the `config.yml` file, `total_rancher_instances`,
which allows you to specify the number of tenant Ranchers to create. 

The default should be two and this will create:

- One Hosted Rancher
- One Tenant Rancher

But if you set this to four `total_rancher_instances: 4` you will get:

- One Hosted Rancher
- Three Tenant Ranchers

### Prerequisites

- **S3 Bucket**: Ensure you have an S3 bucket exclusively for this purpose. It's a one-time setup, reusable for future runs.
- **Configuration File**: Prepare a `config.yml` file as per the Config File Setup section below.

### Job Execution Guidelines

- **S3 Bucket Limitation**: Each S3 bucket can only be associated with one hosted/tenant job at a time.
- **Terraform State File**: Presence of a Terraform state file in the S3 bucket required running a cleanup job before initiating a new one.
- **Creation**: Execute `TestHost` to initiate a hosted Rancher and an imported tenant Rancher setup.

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
  version: 2.11.1
  image: rancher/rancher
  image_tag: v2.11.1
  psp_enabled: false
  env_name_0: ""
  env_value_0: ""
  env_name_1: ""
  env_value_1: ""
k3s:
  version: v1.32.3+k3s1
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
```
