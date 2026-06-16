## stash stash-box batch-studio-tag

StashBoxBatchStudioTag (mutation) [async job]

```
stash stash-box batch-studio-tag [flags]
```

### Options

```
  -h, --help                    help for batch-studio-tag
      --wait                    block until the enqueued job reaches a terminal state; exit 0 on FINISHED, 9 on FAILED/CANCELLED, 10 on --wait-timeout, 11 if the job's outcome cannot be confirmed (re-attach with its id).
      --wait-timeout duration   with --wait, give up after this duration and exit 10 (still-running) with the job id; the default (0) waits indefinitely.
```

### Options inherited from parent commands

```
      --api-key string   Stash API key (default $STASHAPP_API_KEY)
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, ndjson, table, yaml (default "json")
      --url string       Stash base URL (default $STASHAPP_URL)
```

### SEE ALSO

* [stash stash-box](stash_stash-box.md)	 - stash-box operations

