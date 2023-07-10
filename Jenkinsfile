pipeline {
  agent any

  stages {
    stage('Build Docker image') {
      steps {
        script {
          // Write the CONFIG parameter to a file
          writeFile file: 'config.yml', text: params.CONFIG

          // Build the Docker image with ARG for config.yml
          sh 'docker build --build-arg CONFIG_FILE=config.yml -t my-app .'
        }
      }
    }

    stage('Run Tests') {
      steps {
        script {
          // Define dockerImage by building an image or pulling from registry
          def dockerImage = docker.image('my-app') // Assuming 'my-app' is your Docker image name

          dockerImage.inside() {
            sh "go test -v -timeout 1h -run TestCreateHostedTenantRancher -count=1 ./terratest/test"
          }
        }
      }
    }
  }

  post {
    always {
      // Remove the Docker container if it exists
      sh 'docker rm -f my-app || true'
      sh 'docker rmi my-app || true'
      // cleanWs() // unavailable in rancher jenkins
    }
  }
}
