# Hosted / Tenant Rancher

## Setup

There should be a file named `config.yml` that sits at the top level of this repository sitting next to the `README.md`. It should match the following, replaced with your values.

```yml
local:
  pem_path: your-local-path-to-aws-pem-file
rancher:
  bootstrap_password: whatever-rancher-bootstrap-password-you-want
  version: 2.7.4
  image_tag: v2.7.4
  psp_bool: false
k3s:
  version: v1.23.6+k3s1
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
  local_path_aws_pem: your-local-path-to-aws-pem-file
upgrade:
  version: 2.7.4
  image_tag: v2.7-head
```

## Run

In `/terratest/test/host_test.go` run the function `TestHostInfrastructureCreate`. This will create a hosted rancher and tenant rancher that is imported within it. It takes about `~15 minutes` because Terraform/AWS is slow with setting up the two RDS Aurora MySQL databases.

Once finished you'll get the output of the host and tenant Rancher URLs

## Upgrade

You can run the following in `/terratest/test/host_test.go` to upgrade

- `TestUpgradeHostRancher`
- `TestUpgradeTenantRancher`

## Temporary Note

This repository is currently working as an MVP to create a hosted/tenant Rancher fully setup and outputs the URLs for the host and tenant.

It includes a temporary workaround using https://github.com/go-rod/rod because two issues were discovered with Rancher's API / Rancher's Terraform provider while setting this up.

- `github.com/rancher/rancher/issues/39779`
- `github.com/rancher/terraform-provider-rancher2/issues/1042`
