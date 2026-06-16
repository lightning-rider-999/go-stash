## stash gallery list

FindGalleries (query)

```
stash gallery list [flags]
```

### Options

```
      --direction string   list filter: sort direction ASC or DESC (filter.direction)
  -h, --help               help for list
      --id string          convenience: select a single object by ID
      --page string        list filter: page number (filter.page)
      --per-page string    list filter: results per page, -1 for all (filter.per_page)
      --query string       list filter: free-text query (filter.q)
      --sort string        list filter: sort field (filter.sort)
```

### Options inherited from parent commands

```
      --api-key string   Stash API key (default $STASHAPP_API_KEY)
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, ndjson, table, yaml (default "json")
      --url string       Stash base URL (default $STASHAPP_URL)
```

### SEE ALSO

* [stash gallery](stash_gallery.md)	 - gallery operations

