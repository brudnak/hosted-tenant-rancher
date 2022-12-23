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