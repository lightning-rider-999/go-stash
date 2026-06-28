## stash catalog

Print the embedded machine-facing operation catalog

### Synopsis

catalog prints the build-time catalog of every Stash operation: its field, kind, arguments, return type, hazard flags, and exit codes, plus the $defs type dictionary. With no argument it emits the whole catalog verbatim; with an operation name (e.g. `stash catalog FindScenes`) it emits just that entry. No server connection is needed.

```
stash catalog [OpName] [flags]
```

### Options

```
  -h, --help   help for catalog
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

