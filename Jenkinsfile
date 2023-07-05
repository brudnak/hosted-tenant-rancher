pipeline {
  agent any

  stages {
    stage('Build Docker image') {
      steps {
        script {
          // Write the CONFIG parameter to a file
          writeFile file: 'config.yml', text: params.CONFIG

          // Print the contents of config.yml for debugging purposes
          sh 'echo "Print the contents of config.yml for debugging purposes"'
          sh 'cat config.yml'

          // Build the Docker image
          sh 'docker build -t my-app .'

          // Print the current directory for debugging purposes
          sh 'pwd'

          // Run the Docker container with the configuration file
          sh 'docker run -d --name my-app -v $(pwd)/config.yml:/myfolder/config.yml my-app'
        }
      }
    }

    stage('Run Tests') {
      steps {
        script {
          // Define dockerImage by building an image or pulling from registry

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
        // sh 'docker rm -f my-app || true'
        cleanWs()
    }
  }
}
