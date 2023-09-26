# Hosted/Tenant Rancher

This guide walks you through running and managing a Hosted/Tenant Rancher using Jenkins.

## Running in Jenkins

### Prerequisites

1. An existing S3 bucket dedicated to this task.
    - You only need to create this once and can reuse it for all future runs.
    - A Jenkins cleanup job will delete the Terraform state file in the S3 bucket.

2. A completed configuration file (see the Config File Setup section). Copy and paste the YAML into the Jenkins job.

### Time Estimates

The job typically takes around 15 minutes for both creation and deletion. This is primarily due to the time required to spin up and delete the RDS Aurora MySQL databases.

### Job Execution

- Only one hosted/tenant job can be run per S3 bucket.
- If a Terraform state file exists in your S3 bucket, you must run the cleanup job before you can run another job.
- To create more than one hosted/tenant setup simultaneously, provide different S3 bucket names for each. Please note each bucket's name for the cleanup process.

### Cleanup

To run the Hosted/Tenant Cleanup Jenkins Job, use the same configuration file you used to create the setup. The job needs to initialize the state file in the S3 bucket before executing the `terraform destroy` command.

## Config File Setup

A `config.yml` file should be present at the root of the repository, alongside this `README.md`. If running locally, ensure it matches the following template, replacing placeholders with your actual values. If running in Jenkins, paste this YAML into the job.

```yml
s3:
  bucket: name-of-your-s3-bucket-that-you-already-have-created
  region: us-east-2
aws:
  rsa_private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    YOUR-PRIVATE-KEY-HERE
    -----END RSA PRIVATE KEY-----
rancher:
  repository_url: https://releases.rancher.com/server-charts/latest 
  # OR repository_url: https://releases.rancher.com/server-charts/alpha
  # OR repository_url: https://releases.rancher.com/server-charts/stable
  # SEE https://ranchermanager.docs.rancher.com/getting-started/installation-and-upgrade/resources/choose-a-rancher-version#helm-chart-repositories
  bootstrap_password: whatever-rancher-bootstrap-password-you-want
  version: 2.7.5
  image_tag: v2.7.5
  psp_bool: false
k3s:
  version: v1.25.10+k3s1
tf_vars:
  aws_access_key: your-aws-access-key
  aws_secret_key: your-aws-secret-key
  aws_prefix: a-prefix-for-naming-things-must-be-no-more-than-3-characters
  aws_vpc: aws-vpc-you-want-to-use
  aws_subnet_a: subnet-a-id
  aws_subnet_b: subnet-b-id
  aws_subnet_c: subnet-c-id
  aws_ami: the-ami-that-you-want-to-use
  aws_subnet_id: the-subnet-id
  aws_security_group_id: what-security-group-you-want-to-use
  aws_pem_key_name: the-name-of-your-pem-key-in-aws-no-file-extension
  aws_rds_password: password-you-want-for-aws-rds-database-suggest-googling-for-requirements
  aws_route53_fqdn: something.something.something
upgrade:
  version: 2.7.5-rc5
  image_tag: v2.7.5-rc5
```

## Local Execution

To run the process locally, execute the `TestCreateHostedTenantRancher` function in `/terratest/test/host_test.go`. This action creates a hosted Rancher and an imported tenant Rancher. Due to the nature of Terraform/AWS, expect this to take around 15 minutes, mostly spent on setting up the two RDS Aurora MySQL databases.

Upon completion, the system will output the URLs for the host and tenant Ranchers.

## Local Upgrade

To upgrade locally, run the following functions in `/terratest/test/host_test.go`:

- `TestUpgradeHostRancher`
- `TestUpgradeTenantRancher`
