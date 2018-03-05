#!/usr/bin/env bash

DBG_TEST=2
# Debug-level for app
DBG_APP=3
DBG_SRV=2
NBR_SERVERS=10
NBR_SERVERS_GROUP=10

. $(go env GOPATH)/src/gopkg.in/dedis/onet.v2/app/libtest.sh

main(){
    startTest
    buildConode "github.com/dedis/cothority/pulsar/service"
	test App
    stopTest
}

testApp(){
       runCoBG $(seq $NBR_SERVERS)
       testFail runCl random public.toml
       testOK runCl setup -i 100 public.toml
       testOK runCl random public.toml
       sleep 1
       testOK runCl random public.toml
}

testBuild(){
    testOK dbgRun runCl --help
}

runCl(){
    dbgRun ./$APP -d $DBG_APP $@
}

main
