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

You can test with latest, alpha or stable. Just change the rancher.repository_url to what you need. 

More details about repository_url here: https://ranchermanager.docs.rancher.com/getting-started/installation-and-upgrade/resources/choose-a-rancher-version#helm-chart-repositories

## RDS Password

When setting the `aws_rds_password` value in yaml. It needs to adhere to this requirement by AWS:

At least 8 printable ASCII characters. Can't contain any of the following: / (slash), '(single quote), "(double quote) and @ (at sign).

If it doesn't adhere to this, the provisioning of the MySQL Database will fail in Terraform.

```yml
s3:
  bucket: # The name of your S3 Bucket in AWS us-east-2 goes here.
  region: us-east-2
aws:
  # RSA Private Key is the contents of your AWS pem key file.
  rsa_private_key: | 
    -----BEGIN RSA PRIVATE KEY-----
    <<<THE CONTENTS OF YOUR AWS PEM KEY GO HERE>>>
    -----END RSA PRIVATE KEY-----
rancher:
  repository_url: https://releases.rancher.com/server-charts/latest 
  bootstrap_password: # Whatever bootstrap password you want for Rancher goes here.
  version: 2.8.1
  image: rancher/rancher
  image_tag: v2.8-head
  psp_bool: true
k3s:
  version: v1.27.8+k3s2 # 2.6 v1.23.6+k3s1 / 2.7 v1.25.10+k3s1 / 2.8 v1.27.8+k3s2
tf_vars:
  aws_access_key: # Your AWS Access Key
  aws_secret_key: # Your AWS Secret Key
  aws_prefix: # Short prefix for labeling things, should only be 2 or 3 characters, your initials.
  aws_vpc: aws-vpc-you-want-to-use
  aws_subnet_a: subnet-a-id
  aws_subnet_b: subnet-b-id
  aws_subnet_c: subnet-c-id
  aws_ami: the-ami-that-you-want-to-use
  aws_subnet_id: the-subnet-id
  aws_security_group_id: what-security-group-you-want-to-use
  aws_pem_key_name: the-name-of-your-pem-key-in-aws-no-file-extension
  aws_rds_password: # At least 8 printable ASCII characters. Can't contain any of the following: / (slash), '(single quote), "(double quote) and @ (at sign).
  aws_route53_fqdn: something.something.something
  aws_ec2_instance_type: m5.xlarge
upgrade:
  version: ""
  image: rancher/rancher
  image_tag: v2.8-head
```

## Rancher Prime

Added a new yml value `image`. If you want to use Rancher Prime, you need to set the image to `registry.rancher.com/rancher/rancher-prime`. Otherwise, set it as "rancher/rancher".

For Rancher Prime the `repository_url` should be `https://releases.rancher.com/prime-charts/latest`

`image_tag` & `version` can be set as empty strings in the yml if you just want `latest`.

```yml
# OTHER YML CONTINUED ABOVE (THIS IS JUST A SAMPLE SNIPPET)
rancher:
  repository_url: https://releases.rancher.com/prime-charts/latest
  bootstrap_password: your-password-goes-here
  version: ""
  image: registry.rancher.com/rancher/rancher
  image_tag: ""
  psp_bool: true
# OTHER YML CONTINUED BELOW (THIS IS JUST A SAMPLE SNIPPET)
```

## Local Execution

To run the process locally, execute the `TestCreateHostedTenantRancher` function in `/terratest/test/host_test.go`. This action creates a hosted Rancher and an imported tenant Rancher. Due to the nature of Terraform/AWS, expect this to take around 15 minutes, mostly spent on setting up the two RDS Aurora MySQL databases.

Upon completion, the system will output the URLs for the host and tenant Ranchers.

## Local Upgrade

To upgrade locally, run the following functions in `/terratest/test/host_test.go`:

- `TestUpgradeHostRancher`
- `TestUpgradeTenantRancher`
