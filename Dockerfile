# Start from the latest golang base image
FROM golang:1.19

ENV GOPATH /root/go
ENV PATH ${PATH}:/root/go/bin

# Configure Terraform
ARG TERRAFORM_VERSION=1.5.0
RUN wget https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip && apt-get update && apt-get install -y unzip && unzip terraform_${TERRAFORM_VERSION}_linux_amd64.zip && rm terraform_${TERRAFORM_VERSION}_linux_amd64.zip && chmod u+x terraform && mv terraform /usr/bin/terraform

# Install Google Chrome
RUN apt-get update && apt-get install -y wget gnupg2 unzip \
    && wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | apt-key add - \
    && echo "deb http://dl.google.com/linux/chrome/deb/ stable main" >> /etc/apt/sources.list.d/google.list \
    && apt-get update && apt-get install -y google-chrome-stable \
    && rm -rf /var/lib/apt/lists/*

# Install Helm
RUN wget -q https://get.helm.sh/helm-v3.7.0-linux-amd64.tar.gz && \
    tar -xzf helm-v3.7.0-linux-amd64.tar.gz && \
    mv linux-amd64/helm /usr/local/bin/helm && \
    rm -rf helm-v3.7.0-linux-amd64.tar.gz linux-amd64

# Install kubectl
RUN apt-get update && apt-get install -y apt-transport-https ca-certificates curl && \
    curl -s https://packages.cloud.google.com/apt/doc/apt-key.gpg | apt-key add - && \
    echo "deb https://apt.kubernetes.io/ kubernetes-xenial main" | tee /etc/apt/sources.list.d/kubernetes.list && \
    apt-get update && apt-get install -y kubectl && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Set the Current Working Directory inside the container
WORKDIR $GOPATH/src/github.com/brudnak/hosted-tenant-rancher

# Copy go mod and sum files
COPY [".", "$GOPATH/src/github.com/brudnak/hosted-tenant-rancher"]

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source from the current directory to the Working Directory inside the container
COPY . .

# Copy the config file into the container
ARG CONFIG_FILE
COPY ${CONFIG_FILE} /config.yml

# Create the group before creating the user
RUN groupadd -g 112 groupname

RUN useradd -r -u 106 -g 112 jenkins
RUN chmod -R 777 /home

# This container will be executable
SHELL ["/bin/bash", "-c"]