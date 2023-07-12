# Hosted / Tenant Rancher

## Running in Jenkins

##### Prerequisites

You'll need to create a s3 bucket in AWS us-east-2 specifically for this Jenkins job to use.
It will store the Terraform state file.
Naming it something like "<your-initials>-hosted-tenant-tf", and putting that name in the configuration file below.
You'll only be able to create 1 hosted/tenant setup at a time.
If you try running it again, there is a check for an existing state file and it will fail.
So before running the job again,
you'll need to run the Jenkins cleanup job which will run a `terrafor destroy` on the infrastructure,
and also cleanup the state file in the s3 bucket.

##### Estimated Time

The job takes about `~15 minutes` to run for either creation and deletion.
This is because the time it takes to spin up & delete the RDS Aurora MySQL databases.

## Setup

There should be a file named `config.yml` that sits at the top level of this repository sitting next to the `README.md`. It should match the following, replaced with your values.

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

## Run Locally

In `/terratest/test/host_test.go` run the function `TestCreateHostedTenantRancher`.
This will create a hosted rancher and tenant rancher that is imported within it.
It takes about `~15 minutes` because Terraform/AWS is slow with setting up the two RDS Aurora MySQL databases.

Once finished, you'll get the output of the host and tenant Rancher URLs

## Upgrade Locally

You can run the following in `/terratest/test/host_test.go` to upgrade

- `TestUpgradeHostRancher`
- `TestUpgradeTenantRancher`
