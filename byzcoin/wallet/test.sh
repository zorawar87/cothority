#!/usr/bin/env bash

DBG_TEST=2
DBG_SRV=2
DBG_BA=2

NBR_SERVERS=3
NBR_SERVERS_GROUP=3

. "$(go env GOPATH)/src/github.com/dedis/cothority/libtest.sh"

main(){
  startTest
  buildConode github.com/dedis/cothority/byzcoin github.com/dedis/cothority/byzcoin/contracts
  build $APPDIR/../bcadmin
  rm -rf config wallet{1,2}
  mkdir wallet{1,2}
  # run testLoadSave
  run testCoin
  stopTest
}

testLoadSave(){
  rm -f config/*
  runCoBG 1 2 3
  testOK runBA create public.toml --interval .5s
  bc=config/bc*cfg
  testOK runWallet 1 join $bc
}

testCoin(){
  rm -f config/*
  runCoBG 1 2 3
  testOK runBA create public.toml --interval .5s
  bc=config/bc*cfg
  key=config/key*cfg
  testOK runWallet 1 join $bc
  testGrep "Balance is: 0" runWallet 1 show
  runGrepSed "Public key is:" "s/.* //" runWallet 1 show
  PUB=$SED
  runGrepSed "Coin-address is:" "s/.* //" runWallet 1 show
  ACCOUNT=$SED
  testOK runBA mint $bc $key $PUB 1000
  testGrep "Balance is: 1000" runWallet 1 show

  testOK runWallet 2 join $bc
  runGrepSed "Public key is:" "s/.* //" runWallet 2 show
  PUB2=$SED
  testGrep "Balance is: 0" runWallet 2 show
  testFail runWallet 2 transfer 100 $ACCOUNT
  testOK runBA mint $bc $key $PUB2 1000
  testGrep "Balance is: 1000" runWallet 2 show
  testFail runWallet 2 transfer 10000 $ACCOUNT
  testOK runWallet 2 transfer 100 $ACCOUNT
  testGrep "Balance is: 900" runWallet 2 show
  testGrep "Balance is: 1100" runWallet 1 show
}

runBA(){
  ./bcadmin -c config/ --debug $DBG_BA "$@"
}

runWallet(){
  wn=$1
  shift
  ./wallet -c wallet$wn/ --debug $DBG_BA "$@"
}

main
