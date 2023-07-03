pipeline {
  agent any

  stages {
    stage('Build Docker image') {
      steps {
        script {
          // Write the CONFIG parameter to a file
          writeFile file: 'config.yml', text: params.CONFIG

          // Build the Docker image
          sh 'docker build -t my-app .'

          // Run the Docker container with the configuration file
          sh 'docker run -d --name terratest-image -v $(pwd)/config:/ terratest-image'
        }
      }
    }

    stage('Run Tests') {
      steps {
        script {
          // Define dockerImage by building an image or pulling from registry
          def dockerImage = docker.build("my-app")

          dockerImage.inside() {
            sh "go test -v -run TestCreateHostedTenantRancher ./terratest/test"
          }
        }
      }
    }
  }

  post {
    always {
        // Remove the Docker container if it exists
        sh 'docker rm -f my-app || true'
        cleanWs()
    }
  }
}
