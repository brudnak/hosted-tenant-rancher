pipeline {
    agent any

    stages {
        stage('Build Docker image') {
            steps {
                script {
                    dockerImage = docker.build('terratest-image', {
                        args("--build-arg", "CONFIG_FILE=${params.inputFile?.name}")
                    })
            }
        }

        stage('Run Tests') {
            steps {
                script {
                    dockerImage.inside() {
                        sh "cp /workspace/inputFile ../config.yml"
                        sh "-Dorg.jenkinsci.plugins.durabletask.BourneShellScript.LAUNCH_DIAGNOSTICS=true" +  "go test -v -run TestCreateHostedTenantRancher ./terratest/test"
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
