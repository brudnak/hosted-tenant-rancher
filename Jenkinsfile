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
