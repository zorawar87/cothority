pipeline {
  agent any
  stages {
    stage('Build') {
      steps {
        sh '''env
eval `$HOME/bin/gimme 1.11`
go test -p 1 ./...'''
      }
    }
  }
}
