# SkipChain Manager - scmgr

Using the skipchain-manager, you can set up, modify and query skipchains. For an actual application using the skipchains, refer
to [https://github.com/dedis/cothority/cisc].

For it to work, you need the `public.toml` of a running cothority where
you have the right to create a skipchain or add new blocks. If you want only
to test how it works, the simplest way to get up and started is:

```bash
cd cothority/conode
./run_conode.sh local 3
```

This will start three conodes locally and create a new `public.toml` that
you can use with scmgr.

## Creating a new skipchain
