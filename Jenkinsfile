pipeline {
    agent any

    stages {
        stage('Build Docker image') {
            steps {
                script {
                    dockerImage = docker.build('terratest-image')
                }
            }
        }

        stage('Run Tests') {
            steps {
                script {
                    dockerImage.inside() {
                        sh "cp /workspace/config.yml ../config.yml"
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

    parameters {
        file(name: 'inputFile', description: 'Select the file to upload')
    }
}
