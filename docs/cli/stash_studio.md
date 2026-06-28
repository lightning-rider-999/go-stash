## stash studio

studio operations

```
stash studio [flags]
```

### Options

```
  -h, --help   help for studio
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
* [stash studio bulk-update](stash_studio_bulk-update.md)	 - BulkStudioUpdate (mutation)
* [stash studio create](stash_studio_create.md)	 - StudioCreate (mutation)
* [stash studio destroy](stash_studio_destroy.md)	 - StudioDestroy (mutation) [destructive]
* [stash studio destroy-many](stash_studio_destroy-many.md)	 - StudiosDestroy (mutation) [destructive]
* [stash studio get](stash_studio_get.md)	 - FindStudio (query)
* [stash studio list](stash_studio_list.md)	 - FindStudios (query)
* [stash studio update](stash_studio_update.md)	 - StudioUpdate (mutation)

