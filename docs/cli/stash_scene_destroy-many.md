## stash scene destroy-many

ScenesDestroy (mutation) [destructive]

```
stash scene destroy-many [flags]
```

### Options

```
  -h, --help               help for destroy-many
      --yes-i-understand   required to run this DESTRUCTIVE operation: it can drop, overwrite, or anonymise data and cannot be undone. Without it the command refuses and exits 8 (destructive-refused).
```

### Options inherited from parent commands

```
      --api-key string   Stash API key (default $STASHAPP_API_KEY)
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, ndjson, table, yaml (default "json")
      --url string       Stash base URL (default $STASHAPP_URL)
```

### SEE ALSO

* [stash scene](stash_scene.md)	 - scene operations

