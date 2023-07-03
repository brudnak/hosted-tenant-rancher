pipeline {
  agent any

  stages {
    stage('Build Docker image') {
      steps {
        script {
          // Write the CONFIG parameter to a file
          writeFile file: 'config.yml', text: params.CONFIG

          // Build the Docker image
          sh 'docker build -t terratest-image .'

          // Run the Docker container with the configuration file
          sh 'docker run -d --name terratest-image -v $(pwd)/config.yml:/go/src/github.com/brudnak/hosted-tenant-rancher/config.yml terratest-image'
        }
      }
    }

    stage('Run Tests') {
      steps {
        script {
          dockerImage.inside() {
            sh "go test -v -run TestCreateHostedTenantRancher ./terratest/test"
          }
        }
      }
    }
  }

  post {
    always {
      cleanWs()
    }
  }
}
