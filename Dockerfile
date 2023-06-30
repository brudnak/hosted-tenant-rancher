# Start from the latest golang base image
FROM golang:1.19

# Configure Terraform
ARG TERRAFORM_VERSION=1.5.0
RUN wget https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip && apt-get update && apt-get install unzip &&  unzip terraform_${TERRAFORM_VERSION}_linux_amd64.zip && rm terraform_${TERRAFORM_VERSION}_linux_amd64.zip && chmod u+x terraform && mv terraform /usr/bin/terraform

# Install Google Chrome
RUN apt-get update && apt-get install -y wget gnupg2 unzip \
    && wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | apt-key add - \
    && echo "deb http://dl.google.com/linux/chrome/deb/ stable main" >> /etc/apt/sources.list.d/google.list \
    && apt-get update && apt-get install -y google-chrome-stable \
    && rm -rf /var/lib/apt/lists/*

# Set the Current Working Directory inside the container
WORKDIR $GOPATH/src/github.com/brudnak/hosted-tenant-rancher/terratest

# Copy go mod and sum files
COPY [".", "$GOPATH/src/github.com/brudnak/hosted-tenant-rancher"]
#COPY $GOPATH/src/github.com/brudnak/hosted-tenant-rancher/tools/go.mod
#COPY $GOPATH/src/github.com/brudnak/hosted-tenant-rancher/terratest/go.mod

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Create the group before creating the user
RUN groupadd -g 112 groupname

RUN useradd -r -u 106 -g 112 jenkins

# Copy the source from the current directory to the Working Directory inside the container
COPY . .

# This container will be executable
#ENTRYPOINT ["/bin/bash"]
SHELL ["/bin/bash", "-c"]