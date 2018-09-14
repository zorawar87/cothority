def label = "mypod-${UUID.randomUUID().toString()}"
podTemplate(label: label, containers: [
    containerTemplate(name: 'maven', image: 'maven:3.3.9-jdk-8-alpine', ttyEnabled: true, command: 'cat'),
    containerTemplate(name: 'golang', image: 'golang:1.10', ttyEnabled: true, command: 'cat')
  ]) {

  node(label) {
        stage('Go: git') {
            git url: 'https://github.com/dedis/cothority.git'
            container('golang') {
                stage('go test') {
                    sh """
go test -p 1 ./...
                    """
                }
            }
        }
        stage('Java client: git') {
            git 'https://github.com/dedis/cothority.git'
            container('maven') {
                stage('mvn test') {
                    sh """
eval "$(curl -sL https://raw.githubusercontent.com/travis-ci/gimme/master/gimme | GIMME_GO_VERSION=1.11 bash)"
go get github.com/dedis/Coding || true
make test_java
                    """
                }
            }
        }
    }
}
