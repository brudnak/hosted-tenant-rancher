# Hosted / Tenant Rancher

## Setup

There should be a file named `config.yml` that sits at the top level of this repository. It should match the following, replaced with your values.

```yml
local:
  pem_path: local-path-to-your-pem-file-for-aws
rancher:
  bootstrap_password: whatever-bootstrap-password-you-want
  email: email-to-use-in-helm-install-for-lets-encrypt
  version: 2.7.0
  image_tag: v2.7-head
k3s:
  version: v1.24.7+k3s1
```

## Run

In `/terratest/test/host_test.go` run the function `TestHostInfrastructureCreate`. This will create a hosted rancher and tenant rancher that is imported within it. It could take around `~15 minutes` (It's slower because Terraform is slower with setting up AWS RDS Aurora MySQL databases is slow).

Once finished you'll get the output of the host and tenant Rancher URLs

## Additional Terraform Setup

Most of the dynamic Terraform tfvar files are created dynamically. However, you'll need to add your own to `/terratest/modules/aws`

It would look like the following:

```tf
# AWS Access Variables

aws_access_key        = "your-aws-access-key"
aws_secret_key        = "your-aws-secret-key"
aws_prefix            = "your-initials" // this should only be 3 characters!
aws_vpc               = "your-vpc"
aws_subnet_a          = "your-subnet-a"
aws_subnet_b          = "your-subnet-b"
aws_subnet_c          = "your-subnet-c"
aws_ami               = "which-ami-you-want"
aws_subnet_id         = "your-subnet-id"
aws_security_group_id = "your-security-group"
aws_pem_key_name      = "aws-pem-key-name"
aws_rds_password      = "your-rds-password-you-want-to-create" // this has restrictions, suggest googling them 
aws_route53_fqdn      = "your-most-used-route53-fgdn"
local_path_aws_pem    = "local-path-to-your-aws-pem-file"
```