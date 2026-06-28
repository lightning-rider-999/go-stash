## stash performer

performer operations

```
stash performer [flags]
```

### Options

```
  -h, --help   help for performer
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

* [stash](stash.md)	 - Agent-first CLI for the Stash GraphQL API
* [stash performer all](stash_performer_all.md)	 - AllPerformers (query)
* [stash performer bulk-update](stash_performer_bulk-update.md)	 - BulkPerformerUpdate (mutation)
* [stash performer create](stash_performer_create.md)	 - PerformerCreate (mutation)
* [stash performer destroy](stash_performer_destroy.md)	 - PerformerDestroy (mutation) [destructive]
* [stash performer destroy-many](stash_performer_destroy-many.md)	 - PerformersDestroy (mutation) [destructive]
* [stash performer get](stash_performer_get.md)	 - FindPerformer (query)
* [stash performer list](stash_performer_list.md)	 - FindPerformers (query)
* [stash performer merge](stash_performer_merge.md)	 - PerformerMerge (mutation) [destructive]
* [stash performer update](stash_performer_update.md)	 - PerformerUpdate (mutation)

