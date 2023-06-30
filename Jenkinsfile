pipeline {
    agent any

    stages {
        stage('Build Docker image') {
            steps {
                script {
                    dockerImage = docker.build 'terratest-image'
                }
            }
        }

        stage('Run Tests') {
            steps {
                script {
                    dockerImage.inside('-u jenkins') {
                        sh 'go test -v -run TestCreateHostedTenantRancher ./...'
                    }
                }
            }
        }
    }
}
