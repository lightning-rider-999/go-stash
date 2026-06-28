## stash config

config operations

```
stash config [flags]
```

### Options

```
  -h, --help   help for config
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
* [stash config defaults](stash_config_defaults.md)	 - ConfigureDefaults (mutation)
* [stash config dlna](stash_config_dlna.md)	 - ConfigureDLNA (mutation)
* [stash config general](stash_config_general.md)	 - ConfigureGeneral (mutation)
* [stash config get](stash_config_get.md)	 - Configuration (query)
* [stash config interface](stash_config_interface.md)	 - ConfigureInterface (mutation)
* [stash config plugin](stash_config_plugin.md)	 - ConfigurePlugin (mutation)
* [stash config scraping](stash_config_scraping.md)	 - ConfigureScraping (mutation)
* [stash config ui](stash_config_ui.md)	 - ConfigureUI (mutation)
* [stash config ui-setting](stash_config_ui-setting.md)	 - ConfigureUISetting (mutation)

