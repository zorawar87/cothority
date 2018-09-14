pipeline {
  agent any
  stages {
    stage('Build') {
      steps {
        sh '''eval `$HOME/bin/gimme 1.11`
go test -p 1 ./...'''
      }
    }
  }
}