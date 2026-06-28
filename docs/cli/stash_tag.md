## stash tag

tag operations

```
stash tag [flags]
```

### Options

```
  -h, --help   help for tag
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
* [stash tag bulk-update](stash_tag_bulk-update.md)	 - BulkTagUpdate (mutation)
* [stash tag create](stash_tag_create.md)	 - TagCreate (mutation)
* [stash tag destroy](stash_tag_destroy.md)	 - TagDestroy (mutation) [destructive]
* [stash tag destroy-many](stash_tag_destroy-many.md)	 - TagsDestroy (mutation) [destructive]
* [stash tag get](stash_tag_get.md)	 - FindTag (query)
* [stash tag list](stash_tag_list.md)	 - FindTags (query)
* [stash tag merge-many](stash_tag_merge-many.md)	 - TagsMerge (mutation) [destructive]
* [stash tag update](stash_tag_update.md)	 - TagUpdate (mutation)

