pipeline {
  agent any
  stages {
    stage('Build') {
      steps {
        sh 'go test -p 1 ./...'
      }
    }
  }
}