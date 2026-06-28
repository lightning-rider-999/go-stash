## stash metadata clean

MetadataClean (mutation) [destructive, async job]

```
stash metadata clean [flags]
```

### Options

```
  -h, --help                    help for clean
      --wait                    block until the enqueued job reaches a terminal state; exit 0 on FINISHED, 9 on FAILED/CANCELLED, 10 on --wait-timeout, 11 if the job's outcome cannot be confirmed (re-attach with its id).
      --wait-timeout duration   with --wait, give up after this duration and exit 10 (still-running) with the job id; the default (0) waits indefinitely.
      --yes-i-understand        required to run this DESTRUCTIVE operation: it can drop, overwrite, or anonymise data and cannot be undone. Without it the command refuses and exits 8 (destructive-refused).
```

### Options inherited from parent commands

```
      --allow-partial    on an HTTP-200 response that also carries GraphQL errors, still print the partial data to stdout; the error envelope and non-zero exit are unchanged
      --api-key string   Stash API key (default $STASHAPP_API_KEY)
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, ndjson, table, yaml (default "json")
      --url string       Stash base URL (default $STASHAPP_URL)
```

### SEE ALSO

* [stash metadata](stash_metadata.md)	 - metadata operations

