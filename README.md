# Hosted / Tenant Rancher

## Jenkins

Currently able
to run from Jenkins
when using a Multi-line String Parameter
named `CONFIG` and pasting in your version of the `config.yml` file shown below.
You'll need to provide an existing s3 bucket name in your config
when running from Jenkins. This will hold your terraform state file so that you can run cleanup from Jenkins.

## Jenkins Cleanup

Can now run cleanup from Jenkins!
You need an existing s3 bucket when creating / destroying from Jenkins.
However, once you have this bucket, you can always use it,
as the cleanup script also cleans up the terraform state file from s3.

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

## Run

In `/terratest/test/host_test.go` run the function `TestCreateHostedTenantRancher`.
This will create a hosted rancher and tenant rancher that is imported within it.
It takes about `~15 minutes` because Terraform/AWS is slow with setting up the two RDS Aurora MySQL databases.

Once finished, you'll get the output of the host and tenant Rancher URLs

## Upgrade

You can run the following in `/terratest/test/host_test.go` to upgrade

- `TestUpgradeHostRancher`
- `TestUpgradeTenantRancher`

## Temporary Note

This repository includes a temporary workaround
using https://github.com/go-rod/rod
because two issues were discovered with Rancher's API / Rancher's Terraform provider while setting this up.

- `github.com/rancher/rancher/issues/39779`
- `github.com/rancher/terraform-provider-rancher2/issues/1042`
