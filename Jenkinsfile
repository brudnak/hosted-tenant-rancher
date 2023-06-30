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
                    dockerImage.inside('-v $PWD:/workspace') {
                        sh 'cp /workspace/${params.inputFile} /path/to/destination'
                        sh 'go test -v -run TestCreateHostedTenantRancher ./terratest/test'
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
