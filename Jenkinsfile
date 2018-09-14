pipeline {
  agent any
  stages {
    stage('Build') {
      steps {
        sh '''env

$HOME/bin/gimme 1.11
. $HOME/.gimme/envs/go1.11.env

go version
go test -p 1 ./...'''
      }
    }
  }
}