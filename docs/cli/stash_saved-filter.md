## stash saved-filter

saved-filter operations

```
stash saved-filter [flags]
```

### Options

```
  -h, --help   help for saved-filter
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
* [stash saved-filter get](stash_saved-filter_get.md)	 - FindSavedFilter (query)
* [stash saved-filter list](stash_saved-filter_list.md)	 - FindSavedFilters (query)

